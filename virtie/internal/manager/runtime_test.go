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
	runtime := newRuntime(cfg, RuntimePaths{
		ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
		QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
	}, 9, stats, qmp, nil, runtimeDependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)})
	runtime.SetReady()

	status, err := runtime.Status(context.Background(), StatusRequest{})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.State != RuntimeReady || status.CID != 9 || status.Paths.ControlSocket == "" || status.Stats.BootToQMP == "" {
		t.Fatalf("unexpected status: %#v", status)
	}

	resp, err := runtime.Balloon(context.Background(), BalloonRequest{TargetBytes: 768 * testMiB})
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

func TestRuntimeSuspendQueuesAndWaitsForLaunchLoop(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	coordinator := launch.NewSuspendCoordinator()
	qmp := &fakeQMPClient{status: "running"}
	runtime := newRuntime(cfg, RuntimePaths{
		ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
		QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
	}, 9, runtimepkg.NewStats(time.Now()), qmp, coordinator, runtimeDependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)})
	runtime.SetReady()

	done := make(chan error, 1)
	go func() {
		resp, err := runtime.Suspend(context.Background(), SuspendRequest{})
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

func TestRuntimeStartControlServesStatus(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	controlPath := filepath.Join(tmpDir, "virtie.sock")
	runtime := newRuntime(cfg, RuntimePaths{
		ControlSocket: controlPath,
		QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
	}, 11, runtimepkg.NewStats(time.Now()), &fakeQMPClient{}, nil, runtimeDependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)})
	runtime.SetReady()
	if err := runtime.StartControl(context.Background()); err != nil {
		t.Fatalf("start control: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("runtime close: %v", err)
		}
	})

	status, err := control.Dial(controlPath).Status(context.Background(), StatusRequest{})
	if err != nil {
		t.Fatalf("status over control socket: %v", err)
	}
	if status.State != RuntimeReady || status.CID != 11 {
		t.Fatalf("unexpected status: %#v", status)
	}
}

func TestRuntimeWaitUsesConfiguredForegroundCallback(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	processes := runtimepkg.NewProcessSet()
	runtime := newRuntime(cfg, RuntimePaths{
		ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
		QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
	}, 11, runtimepkg.NewStats(time.Now()), &fakeQMPClient{}, nil, runtimeDependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)})
	runtime.SetProcesses(processes, time.Millisecond)

	var called bool
	runtime.SetForegroundWait(&Plan{Manifest: cfg, Options: LaunchOptions{SSH: false}}, func(ctx context.Context, plan *Plan) error {
		called = true
		if !plan.Options.SSH {
			t.Fatalf("wait mode override did not enable ssh: %#v", plan.Options)
		}
		return nil
	})
	if err := runtime.Wait(context.Background(), WaitSSH); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !called {
		t.Fatal("foreground callback was not called")
	}
}

func TestRuntimeInfoUsesConfiguredCollector(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	runtime := newRuntime(cfg, RuntimePaths{
		ControlSocket:    filepath.Join(tmpDir, "virtie.sock"),
		QMPSocket:        filepath.Join(tmpDir, "qmp.sock"),
		GuestAgentSocket: filepath.Join(tmpDir, "qga.sock"),
		SSHReadySocket:   filepath.Join(tmpDir, "ssh-ready.sock"),
	}, 11, runtimepkg.NewStats(time.Now()), &fakeQMPClient{}, nil, runtimeDependencies{
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
	})
	processes := executor.NewGroup()
	processes.Add((&executortest.Process{OverrideName: "qemu-system-x86_64"}).Process())
	runtime.SetWatchers(processes)

	resp, err := runtime.Info(context.Background(), InfoRequest{})
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if resp.ProcessList != "PID COMMAND\n1 init" {
		t.Fatalf("info response: %#v", resp)
	}
}

func TestRuntimeCloseStopsProcessesAndDisconnectsQMPOnce(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	qmp := &fakeQMPClient{}
	process := (&executortest.Process{OverrideName: "qemu-system-x86_64"}).Process()
	processes := runtimepkg.NewProcessSet()
	processes.SetQEMU(process)
	runtime := newRuntime(cfg, RuntimePaths{
		ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
		QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
	}, 11, runtimepkg.NewStats(time.Now()), qmp, nil, runtimeDependencies{QMPTimeout: time.Second, Logger: slog.New(slog.DiscardHandler)})
	runtime.SetProcesses(processes, time.Millisecond)

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
