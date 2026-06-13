package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

func TestStarterFreshLaunchOrdersStartupAndReadiness(t *testing.T) {
	plan := testStarterPlan(t)
	host := &fakeStarterHost{}
	runtimeProvider := &fakeStarterRuntime{events: &host.events}
	starter := Starter{Host: host, Runtime: runtimeProvider}

	started, err := starter.Start(context.Background(), plan)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if started != runtimeProvider.runtime {
		t.Fatalf("started runtime mismatch")
	}
	wantEvents := []string{
		"lock",
		"cid",
		"qemu-command",
		"prepare",
		"start-runs",
		"wait-sockets:virtiofs startup",
		"start-qemu",
		"wait-qmp",
		"install-qmp-shutdown",
		"runtime-suspend-handler",
		"runtime-wait-foreground",
		"runtime-new",
		"runtime-ready",
		"runtime-control",
		"write-guest-files",
		"wait-ssh-ready",
	}
	if !reflect.DeepEqual(host.events, wantEvents) {
		t.Fatalf("events: got %#v want %#v", host.events, wantEvents)
	}
	if plan.CID != 7 || plan.QEMUCommand == nil {
		t.Fatalf("plan was not finalized: cid=%d qemu=%#v", plan.CID, plan.QEMUCommand)
	}
	if got := ControlStats(runtimeProvider.spec.Stats); got.QMPReadyAt.IsZero() || got.FilesReadyAt.IsZero() || got.SSHReadyAt.IsZero() {
		t.Fatalf("expected startup timers in stats: %#v", got)
	}
}

func TestStarterRuntimeNewFailureCleansAcquiredResources(t *testing.T) {
	plan := testStarterPlan(t)
	plan.Manifest.Persistence.StateDir = t.TempDir()
	plan.Manifest.Identity.HostName = "cleanup"
	plan.CleanupFiles = []string{"cleanup.sock"}
	release := &fakeStarterLock{}
	host := &fakeStarterHost{
		lock: &RuntimeLock{
			manifest: plan.Manifest,
			lock:     release,
			pid:      os.Getpid(),
		},
		qmp:               &fakeStarterQMP{},
		recordStatsOutput: true,
	}
	if err := os.WriteFile(LaunchPIDPath(plan.Manifest), []byte(fmt.Sprint(os.Getpid())), 0o644); err != nil {
		t.Fatalf("write launch pid: %v", err)
	}
	runtimeProvider := &fakeStarterRuntime{
		events: &host.events,
		newErr: errors.New("runtime construction failed"),
	}
	starter := Starter{Host: host, Runtime: runtimeProvider}

	_, err := starter.Start(context.Background(), plan)
	if err == nil {
		t.Fatal("expected start error")
	}
	if !release.released {
		t.Fatal("runtime lock was not cleaned up")
	}
	if !host.qmp.(*fakeStarterQMP).disconnected {
		t.Fatal("qmp client was not disconnected")
	}
	for _, event := range []string{"remove-sockets", "stats-output"} {
		if !hasStarterEvent(host.events, event) {
			t.Fatalf("missing cleanup event %q in %#v", event, host.events)
		}
	}
	if _, statErr := os.Stat(LaunchPIDPath(plan.Manifest)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("launch pid cleanup: got %v want not exist", statErr)
	}
}

func TestStarterRejectsNilProviderResults(t *testing.T) {
	tests := []struct {
		name    string
		host    *fakeStarterHost
		runtime *fakeStarterRuntime
		want    string
	}{
		{
			name: "lifecycle",
			host: &fakeStarterHost{
				nilLifecycle: true,
			},
			runtime: &fakeStarterRuntime{},
			want:    "launch host returned nil lifecycle",
		},
		{
			name: "qmp",
			host: &fakeStarterHost{
				nilQMP: true,
			},
			runtime: &fakeStarterRuntime{},
			want:    "launch host returned nil qmp client",
		},
		{
			name:    "suspend handler",
			host:    &fakeStarterHost{},
			runtime: &fakeStarterRuntime{nilSuspendHandler: true},
			want:    "launch runtime returned nil suspend handler",
		},
		{
			name:    "foreground wait",
			host:    &fakeStarterHost{},
			runtime: &fakeStarterRuntime{nilWaitForeground: true},
			want:    "launch runtime returned nil foreground wait",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (Starter{Host: tt.host, Runtime: tt.runtime}).Start(context.Background(), testStarterPlan(t))
			if err == nil {
				t.Fatal("expected start error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error: got %v want containing %q", err, tt.want)
			}
		})
	}
}

func testStarterPlan(t *testing.T) *Plan {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "state")
	return &Plan{
		Manifest: &manifest.Manifest{
			Identity:    manifest.Identity{HostName: "starter-test"},
			Persistence: manifest.Persistence{StateDir: stateDir},
			SSH:         manifest.SSH{Argv: []string{"ssh"}},
		},
		Options:             Options{SSH: true},
		Paths:               RuntimePaths{QMPSocket: "qmp.sock", ControlSocket: "virtie.sock", SSHReadySocket: "ready.sock"},
		VirtioFSSocketPaths: []string{"fs.sock"},
	}
}

type fakeStarterHost struct {
	events            []string
	lock              *RuntimeLock
	qmp               qmpclient.Client
	nilLifecycle      bool
	nilQMP            bool
	recordStatsOutput bool
	prepareErr        error
	startRunsErr      error
	waitQMPErr        error
}

func (h *fakeStarterHost) event(name string) {
	h.events = append(h.events, name)
}

func (h *fakeStarterHost) NewLifecycle(cancel context.CancelFunc) *Lifecycle {
	if h.nilLifecycle {
		return nil
	}
	return NewSignalLifecycle(nil, cancel)
}

func (h *fakeStarterHost) AcquireRuntimeLock(spec RuntimeLockSpec) (*RuntimeLock, error) {
	h.event("lock")
	if h.lock == nil {
		h.lock = &RuntimeLock{
			manifest: spec.Manifest,
			lock:     &fakeStarterLock{},
			pid:      os.Getpid(),
		}
	}
	return h.lock, nil
}

func (h *fakeStarterHost) AcquireCID(*manifest.Manifest, *SuspendState) (int, error) {
	h.event("cid")
	return 7, nil
}

func (h *fakeStarterHost) BuildQEMUCommand(*manifest.Manifest, int, bool) (*exec.Cmd, error) {
	h.event("qemu-command")
	return exec.Command("qemu-system-x86_64"), nil
}

func (h *fakeStarterHost) PrepareRuntimeState(*Plan) error {
	h.event("prepare")
	return h.prepareErr
}

func (h *fakeStarterHost) RemoveSocketPaths([]string) error {
	h.event("remove-sockets")
	return nil
}

func (h *fakeStarterHost) StartRuns(int, *manifest.Manifest) (executor.Group, error) {
	h.event("start-runs")
	return executor.NewGroup(), h.startRunsErr
}

func (h *fakeStarterHost) StartQEMU(*exec.Cmd) (*executor.Process, error) {
	h.event("start-qemu")
	return executor.Wrap(nil), nil
}

func (h *fakeStarterHost) InstallQMPShutdown(*executor.Process, qmpclient.Client) {
	h.event("install-qmp-shutdown")
}

func (h *fakeStarterHost) WaitForSockets(ctx context.Context, stage string, paths []string, watchers executor.Group) error {
	h.event("wait-sockets:" + stage)
	return nil
}

func (h *fakeStarterHost) WaitForQMP(context.Context, string, executor.Group) (qmpclient.Client, error) {
	h.event("wait-qmp")
	if h.nilQMP {
		return nil, nil
	}
	if h.qmp == nil {
		h.qmp = &fakeStarterQMP{}
	}
	return h.qmp, h.waitQMPErr
}

func (h *fakeStarterHost) RestoreRuntime(context.Context, *Plan, qmpclient.Client) error {
	h.event("restore")
	return nil
}

func (h *fakeStarterHost) WriteGuestFiles(context.Context, *Plan, *Stats, executor.Group) error {
	h.event("write-guest-files")
	return nil
}

func (h *fakeStarterHost) WriteBackGuestFiles(context.Context, *Plan, executor.Group) error {
	h.event("write-back-guest-files")
	return nil
}

func (h *fakeStarterHost) WaitForSSHReady(context.Context, string, executor.Group) error {
	h.event("wait-ssh-ready")
	return nil
}

func (h *fakeStarterHost) ShutdownDelay() time.Duration {
	return 0
}

func (h *fakeStarterHost) StatsOutput() io.Writer {
	if h.recordStatsOutput {
		h.event("stats-output")
	}
	return nil
}

type fakeStarterLock struct {
	released bool
}

func (l *fakeStarterLock) Release() error {
	l.released = true
	return nil
}

type fakeStarterQMP struct {
	disconnected bool
}

func (q *fakeStarterQMP) WithRaw(time.Duration, func(*rawQMP.Monitor) error) error { return nil }
func (q *fakeStarterQMP) RunRaw(time.Duration, string) error                       { return nil }
func (q *fakeStarterQMP) DeviceDelAndWait(time.Duration, string) error             { return nil }
func (q *fakeStarterQMP) Stop(time.Duration) error                                 { return nil }
func (q *fakeStarterQMP) Cont(time.Duration) error                                 { return nil }
func (q *fakeStarterQMP) QueryStatus(time.Duration) (string, error)                { return "running", nil }
func (q *fakeStarterQMP) Quit(time.Duration) error                                 { return nil }
func (q *fakeStarterQMP) MigrateToFile(time.Duration, string) error                { return nil }
func (q *fakeStarterQMP) MigrateIncoming(time.Duration, string) error              { return nil }
func (q *fakeStarterQMP) QueryMigrate(time.Duration) (string, error)               { return "completed", nil }
func (q *fakeStarterQMP) Disconnect() error {
	q.disconnected = true
	return nil
}

type fakeStarterRuntime struct {
	events            *[]string
	runtime           *fakeStartedRuntime
	spec              RuntimeSpec
	newErr            error
	nilSuspendHandler bool
	nilWaitForeground bool
}

func (r *fakeStarterRuntime) New(spec RuntimeSpec) (RuntimeResult, error) {
	r.spec = spec
	if r.newErr != nil {
		return RuntimeResult{}, r.newErr
	}
	r.runtime = &fakeStartedRuntime{events: r.events}
	if r.events != nil {
		*r.events = append(*r.events, "runtime-new")
	}
	return RuntimeResult{Runtime: r.runtime, ControlOptions: []control.RouterOption{control.WithHotplug(fakeUnsupportedHotplug{})}}, nil
}

func (r *fakeStarterRuntime) SuspendHandler(SuspendSpec) SuspendHandler {
	if r.nilSuspendHandler {
		return nil
	}
	if r.events != nil {
		*r.events = append(*r.events, "runtime-suspend-handler")
	}
	return fakeSuspendHandler{events: r.events}
}

func (r *fakeStarterRuntime) WaitForeground(ForegroundSpec) func(context.Context, *Plan) error {
	if r.nilWaitForeground {
		return nil
	}
	if r.events != nil {
		*r.events = append(*r.events, "runtime-wait-foreground")
	}
	return func(context.Context, *Plan) error { return nil }
}

type fakeStartedRuntime struct {
	events *[]string
}

func (r *fakeStartedRuntime) SetReady() {
	if r.events != nil {
		*r.events = append(*r.events, "runtime-ready")
	}
}
func (r *fakeStartedRuntime) MarkSavedSuspend()          {}
func (r *fakeStartedRuntime) SetWatchers(executor.Group) {}
func (r *fakeStartedRuntime) StartControl(context.Context, ...control.RouterOption) (*control.Server, error) {
	if r.events != nil {
		*r.events = append(*r.events, "runtime-control")
	}
	return nil, nil
}
func (r *fakeStartedRuntime) Wait(context.Context, WaitMode) error { return nil }
func (r *fakeStartedRuntime) Close() error                         { return nil }
func (r *fakeStartedRuntime) QMP() qmpclient.Client                { return &fakeStarterQMP{} }

type fakeSuspendHandler struct {
	events *[]string
}

func (h fakeSuspendHandler) Handle(context.Context, *SuspendCoordinator) error {
	if h.events != nil {
		*h.events = append(*h.events, "runtime-suspend-handle")
	}
	return nil
}

type fakeUnsupportedHotplug struct{}

func (fakeUnsupportedHotplug) Hotplug(context.Context, control.HotplugRequest) (control.HotplugResponse, error) {
	return control.HotplugResponse{}, nil
}

func hasStarterEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}
