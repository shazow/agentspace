package manager

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	balloonpkg "github.com/shazow/agentspace/virtie/internal/balloontypes"
)

func TestRuntimeStatusAndBalloonUseOwnedQMP(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	cfg.QEMU.Devices.Balloon = &balloonpkg.Device{ID: "balloon0", Transport: "pci"}
	stats := newLaunchStats(time.Now())
	stats.MarkBootStarted(time.Now())
	stats.MarkQMPReady(time.Now())
	qmp := (&fakeQMPClient{queryBalloonActualBytes: 640 * testMiB}).withDefaultBalloonPath("/machine/peripheral/balloon0")
	runtime := newRuntime(&manager{logger: slog.New(slog.DiscardHandler), qmpConnectTimeout: time.Second}, cfg, RuntimePaths{
		ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
		QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
	}, 9, stats, qmp, nil)
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
	coordinator := newLaunchSuspendCoordinator()
	qmp := &fakeQMPClient{status: "running"}
	runtime := newRuntime(&manager{logger: slog.New(slog.DiscardHandler)}, cfg, RuntimePaths{
		ControlSocket: filepath.Join(tmpDir, "virtie.sock"),
		QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
	}, 9, newLaunchStats(time.Now()), qmp, coordinator)
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
	runtime := newRuntime(&manager{logger: slog.New(slog.DiscardHandler)}, cfg, RuntimePaths{
		ControlSocket: controlPath,
		QMPSocket:     filepath.Join(tmpDir, "qmp.sock"),
	}, 11, newLaunchStats(time.Now()), &fakeQMPClient{}, nil)
	runtime.SetReady()
	if err := runtime.StartControl(context.Background()); err != nil {
		t.Fatalf("start control: %v", err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("runtime close: %v", err)
		}
	})

	status, err := Dial(controlPath).Status(context.Background(), StatusRequest{})
	if err != nil {
		t.Fatalf("status over control socket: %v", err)
	}
	if status.State != RuntimeReady || status.CID != 11 {
		t.Fatalf("unexpected status: %#v", status)
	}
}
