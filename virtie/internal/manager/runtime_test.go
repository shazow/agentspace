package manager

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	balloonpkg "github.com/shazow/agentspace/virtie/internal/balloontypes"
	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/executor/executortest"
	control "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
)

func TestRuntimeStatusAndBalloonUseOwnedQMP(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	cfg.QEMU.Devices.Balloon = &balloonpkg.Device{ID: "balloon0", Transport: "pci"}
	stats := runtimepkg.NewStats(time.Now())
	stats.MarkBootStarted(time.Now())
	stats.MarkQMPReady(time.Now())
	qmp := (&fakeQMPClient{queryBalloonActualBytes: 640 * testMiB}).withDefaultBalloonPath("/machine/peripheral/balloon0")
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Paths: launch.RuntimePaths{
			ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
		},
		CID:          9,
		Stats:        stats,
		QMP:          qmp,
		Dependencies: runtimepkg.Dependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)},
	})
	runtime.SetReady()

	status, err := runtime.Status(context.Background(), control.StatusRequest{})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.State != control.RuntimeReady || status.CID != 9 || status.Paths.ControlSocket == "" || status.Stats.BootToQMP == "" {
		t.Fatalf("unexpected status: %#v", status)
	}

	resp, err := runtime.Balloon(context.Background(), control.BalloonRequest{TargetBytes: 768 * testMiB})
	if err != nil {
		t.Fatalf("balloon: %v", err)
	}
	if resp.ActualBytes != 640*testMiB || resp.TargetBytes != 768*testMiB {
		t.Fatalf("unexpected balloon response: %#v", resp)
	}
	if got := qmp.setBalloonLogicalSizes; len(got) != 1 || got[0] != 768*testMiB {
		t.Fatalf("expected balloon resize through qmp, got %#v", got)
	}
}

func TestRuntimeBalloonMapsMissingDeviceToFailedPrecondition(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Paths: launch.RuntimePaths{
			ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
		},
		CID:          9,
		Stats:        runtimepkg.NewStats(time.Now()),
		QMP:          &fakeQMPClient{},
		Dependencies: runtimepkg.Dependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)},
	})

	_, err := runtime.Balloon(context.Background(), control.BalloonRequest{})
	var rpcErr *control.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error type: got %T", err)
	}
	if rpcErr.Code != control.ErrFailedPrecondition {
		t.Fatalf("code: got %s want %s", rpcErr.Code, control.ErrFailedPrecondition)
	}
}

func TestRuntimeSuspendQueuesAndWaitsForLaunchLoop(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	coordinator := launch.NewSuspendCoordinator()
	qmp := &fakeQMPClient{status: "running"}
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Paths: launch.RuntimePaths{
			ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
		},
		CID:             9,
		Stats:           runtimepkg.NewStats(time.Now()),
		QMP:             qmp,
		SuspendRequests: coordinator,
		Dependencies: runtimepkg.Dependencies{
			QMPTimeout:       time.Second,
			Logger:           slog.New(slog.DiscardHandler),
			SavedSuspendExit: isSavedSuspendExit,
		},
	})
	runtime.SetReady()

	done := make(chan error, 1)
	go func() {
		resp, err := runtime.Suspend(context.Background(), control.SuspendRequest{})
		if err == nil && !resp.Saved {
			err = errors.New("suspend response was not saved")
		}
		done <- err
	}()

	select {
	case <-coordinator.Notify():
	case err := <-done:
		t.Fatalf("suspend returned before launch loop handled request: %v", err)
	case <-time.After(time.Second):
		t.Fatal("suspend request was not queued")
	}
	select {
	case err := <-done:
		t.Fatalf("suspend returned before launch loop completion: %v", err)
	case <-time.After(testNoReturnTimeout):
	}
	if qmp.migrateCalls != 0 {
		t.Fatalf("runtime suspend migrated directly over qmp, got %d calls", qmp.migrateCalls)
	}

	coordinator.Begin()
	coordinator.Complete(errSavedSuspendExit)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("suspend: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("suspend did not return after launch loop completion")
	}
}

func TestRuntimeSuspendMapsMissingCoordinatorToFailedPrecondition(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Paths: launch.RuntimePaths{
			ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
		},
		CID:          9,
		Stats:        runtimepkg.NewStats(time.Now()),
		QMP:          &fakeQMPClient{},
		Dependencies: runtimepkg.Dependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)},
	})
	runtime.SetReady()

	_, err := runtime.Suspend(context.Background(), control.SuspendRequest{})
	var rpcErr *control.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error type: got %T", err)
	}
	if rpcErr.Code != control.ErrFailedPrecondition {
		t.Fatalf("code: got %s want %s", rpcErr.Code, control.ErrFailedPrecondition)
	}
}

func TestRuntimeStartControlServesStatus(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	controlPath := filepath.Join(tmpDir, "virtie.sock")
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Paths: launch.RuntimePaths{
			ControlSocket: controlPath,
			QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
		},
		CID:          11,
		Stats:        runtimepkg.NewStats(time.Now()),
		QMP:          &fakeQMPClient{},
		Dependencies: runtimepkg.Dependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)},
	})
	runtime.SetReady()
	if _, err := runtime.StartControl(context.Background()); err != nil {
		t.Fatalf("start control: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("runtime close: %v", err)
		}
	})

	status, err := control.Dial(controlPath).Status(context.Background(), control.StatusRequest{})
	if err != nil {
		t.Fatalf("status over control socket: %v", err)
	}
	if status.State != control.RuntimeReady || status.CID != 11 {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestRuntimeWaitUsesConfiguredForegroundCallback(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	processes := runtimepkg.NewProcessSet()
	var called bool
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Plan:     &launch.Plan{Manifest: cfg, Options: LaunchOptions{SSH: false}},
		Paths: launch.RuntimePaths{
			ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
		},
		CID:           11,
		Stats:         runtimepkg.NewStats(time.Now()),
		QMP:           &fakeQMPClient{},
		Processes:     processes,
		ShutdownDelay: time.Millisecond,
		WaitForeground: func(ctx context.Context, plan *launch.Plan) error {
			called = true
			if !plan.Options.SSH {
				t.Fatalf("wait mode override did not enable ssh: %#v", plan.Options)
			}
			return nil
		},
		Dependencies: runtimepkg.Dependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)},
	})
	if err := runtime.Wait(context.Background(), WaitSSH); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !called {
		t.Fatal("foreground callback was not called")
	}
}

func TestRuntimeWaitMapsMissingForegroundToFailedPrecondition(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Paths: launch.RuntimePaths{
			ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
		},
		CID:          11,
		Stats:        runtimepkg.NewStats(time.Now()),
		QMP:          &fakeQMPClient{},
		Dependencies: runtimepkg.Dependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)},
	})

	err := runtime.Wait(context.Background(), WaitSSH)
	var rpcErr *control.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error type: got %T", err)
	}
	if rpcErr.Code != control.ErrFailedPrecondition {
		t.Fatalf("code: got %s want %s", rpcErr.Code, control.ErrFailedPrecondition)
	}
}

func TestRuntimeSavedSuspendWaitSkipsCloseWriteBack(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	processes := runtimepkg.NewProcessSet()
	qmp := &fakeQMPClient{}
	var writeBackCalls int
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Plan:     &launch.Plan{Manifest: cfg, Options: LaunchOptions{SSH: false}},
		Paths: launch.RuntimePaths{
			ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
		},
		CID:           11,
		Stats:         runtimepkg.NewStats(time.Now()),
		QMP:           qmp,
		Processes:     processes,
		ShutdownDelay: time.Millisecond,
		WaitForeground: func(context.Context, *launch.Plan) error {
			return errSavedSuspendExit
		},
		CloseHooks: runtimepkg.CloseHooks{
			WriteBack: func(context.Context) error {
				writeBackCalls++
				return nil
			},
		},
		Dependencies: runtimepkg.Dependencies{
			QMPTimeout:       time.Second,
			Logger:           slog.New(slog.DiscardHandler),
			SavedSuspendExit: isSavedSuspendExit,
		},
	})

	if err := runtime.Wait(context.Background(), WaitVM); !errors.Is(err, errSavedSuspendExit) {
		t.Fatalf("wait error: got %v want %v", err, errSavedSuspendExit)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if writeBackCalls != 0 {
		t.Fatalf("write-back calls: got %d want 0", writeBackCalls)
	}
}

func TestRuntimeInfoUsesConfiguredCollector(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Paths: launch.RuntimePaths{
			ControlSocket:    filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:        filepath.Join(tmpDir, "qmp.sock"),
			GuestAgentSocket: filepath.Join(tmpDir, "qga.sock"),
			SSHReadySocket:   filepath.Join(tmpDir, "ssh-ready.sock"),
		},
		CID:   11,
		Stats: runtimepkg.NewStats(time.Now()),
		QMP:   &fakeQMPClient{},
		Dependencies: runtimepkg.Dependencies{
			QMPTimeout: time.Second,
			Logger:     slog.New(slog.DiscardHandler),
			CollectInfo: func(ctx context.Context, socketPath string, watchers executor.Group) (runtimepkg.GuestInfo, error) {
				if socketPath != filepath.Join(tmpDir, "qga.sock") {
					t.Fatalf("socket path: got %q", socketPath)
				}
				if watchers.Len() != 1 {
					t.Fatalf("watchers: got %d want 1", watchers.Len())
				}
				return runtimepkg.GuestInfo{ProcessList: "PID COMMAND\n1 init"}, nil
			},
		},
	})
	processes := executor.NewGroup()
	processes.Add((&executortest.Process{OverrideName: "qemu-system-x86_64"}).Process())
	runtime.SetWatchers(processes)

	resp, err := runtime.Info(context.Background(), control.InfoRequest{})
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if resp.ProcessList != "PID COMMAND\n1 init" {
		t.Fatalf("info response: %#v", resp)
	}
}

func TestRuntimeInfoMapsCollectorErrorToFailedPrecondition(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	collectErr := errors.New("guest agent failed")
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Paths: launch.RuntimePaths{
			ControlSocket:    filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:        filepath.Join(tmpDir, "qmp.sock"),
			GuestAgentSocket: filepath.Join(tmpDir, "qga.sock"),
		},
		CID:   11,
		Stats: runtimepkg.NewStats(time.Now()),
		QMP:   &fakeQMPClient{},
		Dependencies: runtimepkg.Dependencies{
			QMPTimeout: time.Second,
			Logger:     slog.New(slog.DiscardHandler),
			CollectInfo: func(context.Context, string, executor.Group) (runtimepkg.GuestInfo, error) {
				return runtimepkg.GuestInfo{}, collectErr
			},
		},
	})

	_, err := runtime.Info(context.Background(), control.InfoRequest{})
	var rpcErr *control.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error type: got %T", err)
	}
	if rpcErr.Code != control.ErrFailedPrecondition || rpcErr.Message != collectErr.Error() {
		t.Fatalf("rpc error: %#v", rpcErr)
	}
}

func TestRuntimeCloseStopsProcessesAndDisconnectsQMPOnce(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	qmp := &fakeQMPClient{}
	process := (&executortest.Process{OverrideName: "qemu-system-x86_64"}).Process()
	processes := runtimepkg.NewProcessSet()
	processes.SetQEMU(process)
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest: cfg,
		Paths: launch.RuntimePaths{
			ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
			QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
		},
		CID:           11,
		Stats:         runtimepkg.NewStats(time.Now()),
		QMP:           qmp,
		Processes:     processes,
		ShutdownDelay: time.Millisecond,
		Dependencies:  runtimepkg.Dependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)},
	})

	if err := runtime.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if got, want := qmp.disconnectCalls, 1; got != want {
		t.Fatalf("unexpected qmp disconnect calls: got %d want %d", got, want)
	}
	if exited, err := process.PollExit(); err != nil || !exited {
		t.Fatalf("expected process to exit after close, exited=%v err=%v", exited, err)
	}
}
