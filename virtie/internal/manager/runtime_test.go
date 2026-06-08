package manager

import (
	"context"
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
