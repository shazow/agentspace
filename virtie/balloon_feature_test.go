package virtie

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	balloonpkg "github.com/shazow/agentspace/virtie/balloon"
)

func TestBuildQEMUSpecAppendsBalloonFeatureArgs(t *testing.T) {
	manifest := validManifestWithBalloon("/tmp/work")
	manifest.QEMU.Devices.Balloon.DeflateOnOOM = true
	manifest.QEMU.Devices.Balloon.FreePageReporting = true

	spec, err := buildQEMUSpec(manifest, 42)
	if err != nil {
		t.Fatalf("build qemu spec: %v", err)
	}
	if !containsString(spec.Args, "virtio-balloon-pci,id=balloon0,deflate-on-oom=on,free-page-reporting=on") {
		t.Fatalf("expected qemu args to include balloon device: %v", spec.Args)
	}
}

func TestManagerLaunchStartsBalloonControllerAfterSSHReadinessAndStopsItBeforeQuit(t *testing.T) {
	tmpDir := t.TempDir()
	manifest := validManifestWithBalloon(tmpDir)
	manifest.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")
	manifest.QEMU.Devices.Balloon.Controller = &balloonpkg.ControllerConfig{
		PollIntervalSeconds:   1,
		ReclaimHoldoffSeconds: 1,
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &fakeRunner{
		cancel:      cancel,
		cancelDelay: 2 * time.Second,
	}

	var enableProbes int
	var quitAt time.Time
	qmpClient := (&fakeQMPClient{
		onQuit: func() {
			quitAt = time.Now()
			runner.exitQEMU(nil)
		},
		onEnableBalloonStats: func() {
			runner.mu.Lock()
			enableProbes = runner.probes
			runner.mu.Unlock()
		},
		readBalloonStats: balloonpkg.Stats{
			Stats: map[string]int64{
				"stat-available-memory": int64(900) * balloonpkg.BytesPerMiB,
			},
			LastUpdate: time.Now(),
		},
		readBalloonStatsDelay: 400 * time.Millisecond,
		queryBalloonInfo:      balloonpkg.Info{ActualBytes: int64(512) * balloonpkg.BytesPerMiB},
	}).withDefaultBalloonPath("/machine/peripheral/balloon0")

	waiter := &fakeSocketWaiter{
		callback: func(paths []string) error {
			for _, path := range paths {
				file, err := os.Create(path)
				if err != nil {
					return err
				}
				file.Close()
			}
			return nil
		},
	}

	manager := &Manager{
		Locker:            &FileLocker{},
		Runner:            runner,
		SocketWaiter:      waiter,
		QMPDialer:         &fakeQMPDialer{client: qmpClient},
		Logger:            log.New(io.Discard, "", 0),
		SSHRetryDelay:     0,
		ShutdownDelay:     10 * time.Millisecond,
		QMPRetryDelay:     0,
		QMPConnectTimeout: time.Second,
		QMPQuitTimeout:    time.Second,
	}

	err := manager.Launch(cancelCtx, manifest, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	if enableProbes < 3 {
		t.Fatalf("expected balloon controller to start only after ssh readiness succeeded, got probe count %d", enableProbes)
	}
	if got, want := qmpClient.quitCount(), 1; got != want {
		t.Fatalf("expected qmp quit to be called once, got %d", got)
	}

	readCompleted := qmpClient.readCompletionTime()
	if readCompleted.IsZero() {
		t.Fatal("expected balloon controller poll to run before shutdown")
	}
	if quitAt.Before(readCompleted) {
		t.Fatalf("expected qmp quit after balloon controller stopped: quit=%s read-complete=%s", quitAt, readCompleted)
	}
}

func TestManagerLaunchDoesNotAbortOnBalloonControllerFailure(t *testing.T) {
	tmpDir := t.TempDir()
	manifest := validManifestWithBalloon(tmpDir)
	manifest.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")

	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &fakeRunner{cancel: cancel}
	qmpClient := (&fakeQMPClient{
		enableBalloonStatsErr: errors.New("guest stats unavailable"),
		onQuit: func() {
			runner.exitQEMU(nil)
		},
	}).withDefaultBalloonPath("/machine/peripheral/balloon0")

	waiter := &fakeSocketWaiter{
		callback: func(paths []string) error {
			for _, path := range paths {
				file, err := os.Create(path)
				if err != nil {
					return err
				}
				file.Close()
			}
			return nil
		},
	}

	manager := &Manager{
		Locker:            &FileLocker{},
		Runner:            runner,
		SocketWaiter:      waiter,
		QMPDialer:         &fakeQMPDialer{client: qmpClient},
		Logger:            log.New(io.Discard, "", 0),
		SSHRetryDelay:     0,
		ShutdownDelay:     10 * time.Millisecond,
		QMPRetryDelay:     0,
		QMPConnectTimeout: time.Second,
		QMPQuitTimeout:    time.Second,
	}

	err := manager.Launch(cancelCtx, manifest, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	if got, want := len(runner.sshArgs), 4; got != want {
		t.Fatalf("expected ssh session to start despite balloon controller failure, got %d ssh starts", got)
	}
	if got, want := qmpClient.quitCount(), 1; got != want {
		t.Fatalf("expected qmp quit to still be used on teardown, got %d", got)
	}
}

func TestBalloonControllerTaskWithNilLoggerDoesNotPanicOnFailure(t *testing.T) {
	qmpClient := (&fakeQMPClient{
		enableBalloonStatsErr: errors.New("guest stats unavailable"),
	}).withDefaultBalloonPath("/machine/peripheral/balloon0")
	task := balloonpkg.ControllerTask(nil, time.Second, qmpClient, &balloonpkg.Device{
		ID:        "balloon0",
		Transport: "pci",
		Controller: &balloonpkg.ControllerConfig{
			MinActualMiB:             512,
			MaxActualMiB:             1024,
			GrowBelowAvailableMiB:    256,
			ReclaimAboveAvailableMiB: 512,
			StepMiB:                  256,
			PollIntervalSeconds:      1,
			ReclaimHoldoffSeconds:    1,
		},
	})
	if task == nil {
		t.Fatal("expected balloon controller task")
	}

	if err := task(context.Background()); err != nil {
		t.Fatalf("expected nil task error, got %v", err)
	}
}

func TestBalloonControllerTaskWithNilLoggerDoesNotPanicOnAdjustment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	qmpClient := (&fakeQMPClient{
		readBalloonStats: balloonpkg.Stats{
			Stats: map[string]int64{
				"stat-available-memory": int64(500) * balloonpkg.BytesPerMiB,
			},
			LastUpdate: time.Now(),
		},
		queryBalloonInfo: balloonpkg.Info{ActualBytes: int64(512) * balloonpkg.BytesPerMiB},
	}).withDefaultBalloonPath("/machine/peripheral/balloon0")
	task := balloonpkg.ControllerTask(nil, time.Second, qmpClient, &balloonpkg.Device{
		ID:        "balloon0",
		Transport: "pci",
		Controller: &balloonpkg.ControllerConfig{
			MinActualMiB:             512,
			MaxActualMiB:             1024,
			GrowBelowAvailableMiB:    600,
			ReclaimAboveAvailableMiB: 900,
			StepMiB:                  256,
			PollIntervalSeconds:      1,
			ReclaimHoldoffSeconds:    1,
		},
	})
	if task == nil {
		t.Fatal("expected balloon controller task")
	}

	done := make(chan error, 1)
	go func() {
		done <- task(ctx)
	}()
	time.Sleep(1100 * time.Millisecond)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("expected nil task error, got %v", err)
	}
	if got := len(qmpClient.setBalloonLogicalSizes); got == 0 {
		t.Fatal("expected balloon controller to adjust guest memory")
	}
}
