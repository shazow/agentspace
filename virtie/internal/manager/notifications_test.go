package manager

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestCommandNotifierHonorsStateAllowlistAndPassesEnv(t *testing.T) {
	cfg := validManifest("/tmp/work")
	cfg.Notifications = manifest.Notifications{
		Command: &manifest.Command{
			Path: "bin/notify",
			Args: []string{"--flag"},
		},
		States: []string{notifyStateRuntimeResume},
	}
	runner := &recordingNotificationRunner{}
	notifier := newCommandNotifier(cfg, log.New(os.Stderr, "", 0), runner)

	notifier.Notify(context.Background(), notifyStateRuntimeSuspend, "ignored", nil)
	if len(runner.calls) != 0 {
		t.Fatalf("expected allowlist to suppress suspend notification, got %#v", runner.calls)
	}

	notifier.Notify(context.Background(), notifyStateRuntimeResume, "Restored", map[string]string{
		"vmStatePath": "/tmp/work/state",
		"cid":         "7",
	})
	if got, want := len(runner.calls), 1; got != want {
		t.Fatalf("expected one notification command, got %d", got)
	}
	call := runner.calls[0]
	if got, want := call.path, "/tmp/work/bin/notify"; got != want {
		t.Fatalf("unexpected command path: got %q want %q", got, want)
	}
	if got, want := call.args, []string{"--flag"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected command args: got %v want %v", got, want)
	}
	if got, want := call.dir, "/tmp/work"; got != want {
		t.Fatalf("unexpected command dir: got %q want %q", got, want)
	}
	for _, want := range []string{
		"VIRTIE_NOTIFY_STATE=runtime:resume",
		"VIRTIE_NOTIFY_MESSAGE=Restored",
		"VIRTIE_NOTIFY_CONTEXT_CID=7",
		"VIRTIE_NOTIFY_CONTEXT_VM_STATE_PATH=/tmp/work/state",
	} {
		if !slices.Contains(call.env, want) {
			t.Fatalf("expected env %q in %#v", want, call.env)
		}
	}
}

func TestCommandNotifierResolvesRelativeCommandAgainstAbsoluteWorkingDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	cfg := validManifest("work")
	cfg.Notifications.Command = &manifest.Command{Path: "bin/notify"}
	runner := &recordingNotificationRunner{}
	notifier := newCommandNotifier(cfg, log.New(os.Stderr, "", 0), runner)

	notifier.Notify(context.Background(), notifyStateRuntimeResume, "Restored", nil)

	if got, want := len(runner.calls), 1; got != want {
		t.Fatalf("expected one notification command, got %d", got)
	}
	call := runner.calls[0]
	if got, want := call.dir, filepath.Join(tmpDir, "work"); got != want {
		t.Fatalf("unexpected command dir: got %q want %q", got, want)
	}
	if got, want := call.path, filepath.Join(tmpDir, "work", "bin", "notify"); got != want {
		t.Fatalf("unexpected command path: got %q want %q", got, want)
	}
}

func TestCommandNotifierLogsAndIgnoresHookFailure(t *testing.T) {
	cfg := validManifest("/tmp/work")
	cfg.Notifications.Command = &manifest.Command{Path: "/bin/notify"}
	runner := &recordingNotificationRunner{err: errors.New("exit status 1")}
	var logs bytes.Buffer
	notifier := newCommandNotifier(cfg, log.New(&logs, "", 0), runner)

	notifier.Notify(context.Background(), notifyStateRuntimeResume, "Restored", nil)

	if got, want := len(runner.calls), 1; got != want {
		t.Fatalf("expected one notification command, got %d", got)
	}
	if !strings.Contains(logs.String(), "notification hook failed for runtime:resume") {
		t.Fatalf("expected hook failure log, got %q", logs.String())
	}
}

func TestSaveSuspendStateConnectedNotifiesAfterSavedStateWrite(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	cfg.QEMU.QMP.SocketPath = "qmp.sock"
	qmpSocketPath := filepath.Join(tmpDir, "qmp.sock")
	qmpClient := &fakeQMPClient{status: "running"}
	manager := &manager{qmpConnectTimeout: time.Millisecond}

	notifier := &recordingNotifier{
		onNotify: func() {
			if _, err := os.Stat(suspendStatePath(cfg)); err != nil {
				t.Fatalf("expected suspend state to exist before notification: %v", err)
			}
		},
	}
	if err := manager.saveSuspendStateConnected(context.Background(), cfg, qmpSocketPath, qmpClient, 7, notifier); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	if got, want := len(notifier.calls), 1; got != want {
		t.Fatalf("expected one notification, got %d", got)
	}
	call := notifier.calls[0]
	if call.state != notifyStateRuntimeSuspend {
		t.Fatalf("unexpected notification state: got %q", call.state)
	}
	if call.values["vm_state_path"] != vmStatePath(cfg) || call.values["cid"] != "7" {
		t.Fatalf("unexpected notification values: %#v", call.values)
	}
}

func TestLaunchResumeNotifiesAfterMigrationAndContinue(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	cfg.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")
	cfg.Volumes[0].AutoCreate = false
	statePath := vmStatePath(cfg)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("saved state"), 0o644); err != nil {
		t.Fatalf("write vm state: %v", err)
	}
	if err := writeSuspendStateData(cfg, suspendState{
		QMPSocketPath: filepath.Join(tmpDir, "qmp.sock"),
		VMStatePath:   statePath,
		CID:           3,
		Status:        "saved",
	}); err != nil {
		t.Fatalf("write suspend state: %v", err)
	}

	runner := &fakeRunner{finishInteractiveSSH: true}
	qmpClient := &fakeQMPClient{
		status: "paused",
		onQuit: func() {
			runner.exitQEMU(nil)
		},
	}
	notifier := &recordingNotifier{
		onNotify: func() {
			qmpClient.mu.Lock()
			defer qmpClient.mu.Unlock()
			if qmpClient.migrateIncomingCalls != 1 || qmpClient.contCalls != 1 {
				t.Fatalf("notification fired before restore completed: migrate-incoming=%d cont=%d", qmpClient.migrateIncomingCalls, qmpClient.contCalls)
			}
		},
	}
	manager := &manager{
		locker:              &fileLocker{},
		runner:              runner,
		socketWaiter:        &fakeSocketWaiter{callback: func(paths []string) error { return nil }},
		qmpDialer:           &fakeQMPDialer{client: qmpClient},
		logger:              log.New(os.Stderr, "", 0),
		sshRetryDelay:       0,
		shutdownDelay:       10 * time.Millisecond,
		qmpConnectTimeout:   time.Millisecond,
		qmpQuitTimeout:      time.Millisecond,
		qmpMigrationTimeout: time.Second,
		notifier:            notifier,
	}

	if err := manager.launchWithOptions(context.Background(), cfg, nil, LaunchOptions{Resume: ResumeModeForce}); err != nil {
		t.Fatalf("launch resume: %v", err)
	}

	if got, want := len(notifier.calls), 1; got != want {
		t.Fatalf("expected one notification, got %d", got)
	}
	call := notifier.calls[0]
	if call.state != notifyStateRuntimeResume {
		t.Fatalf("unexpected notification state: got %q", call.state)
	}
	if call.values["vm_state_path"] != statePath || call.values["cid"] != "3" {
		t.Fatalf("unexpected notification values: %#v", call.values)
	}
}

type notificationRunnerCall struct {
	path string
	args []string
	dir  string
	env  []string
}

type recordingNotificationRunner struct {
	calls []notificationRunnerCall
	err   error
}

func (r *recordingNotificationRunner) Run(ctx context.Context, path string, args []string, dir string, env []string) error {
	r.calls = append(r.calls, notificationRunnerCall{
		path: path,
		args: append([]string(nil), args...),
		dir:  dir,
		env:  append([]string(nil), env...),
	})
	return r.err
}

type recordingNotification struct {
	state   string
	message string
	values  map[string]string
}

type recordingNotifier struct {
	calls    []recordingNotification
	onNotify func()
}

func (n *recordingNotifier) Notify(ctx context.Context, state string, message string, values map[string]string) {
	if n.onNotify != nil {
		n.onNotify()
	}
	n.calls = append(n.calls, recordingNotification{
		state:   state,
		message: message,
		values:  values,
	})
}
