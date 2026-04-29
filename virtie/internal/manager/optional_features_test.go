package manager

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	govmmQemu "github.com/kata-containers/govmm/qemu"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type fakeOptionalFeature struct {
	appendMarker           string
	runner                 *fakeRunner
	startedAfterSSHSession int
	stoppedAt              time.Time
}

func TestBuildQEMUSpecAppendsOptionalFeatureArgs(t *testing.T) {
	feature := &fakeOptionalFeature{appendMarker: "fake-feature-device"}
	setOptionalFeaturesForTest(t, feature)

	spec, err := buildQEMUSpec(validManifest("/tmp/work"), 42)
	if err != nil {
		t.Fatalf("build qemu spec: %v", err)
	}
	if !containsString(spec.Args, feature.appendMarker) {
		t.Fatalf("expected qemu args to include optional feature marker %q: %v", feature.appendMarker, spec.Args)
	}
}

func TestManagerLaunchStartsOptionalFeatureBeforeSSHSessionAndStopsItBeforeQuit(t *testing.T) {
	tmpDir := t.TempDir()
	manifest := validManifest(tmpDir)
	manifest.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")

	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &fakeRunner{
		cancel:      cancel,
		cancelDelay: 2 * time.Second,
	}
	feature := &fakeOptionalFeature{runner: runner}
	setOptionalFeaturesForTest(t, feature)

	var quitAt time.Time
	qmpClient := &fakeQMPClient{
		onQuit: func() {
			quitAt = time.Now()
			runner.exitQEMU(nil)
		},
	}
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

	manager := &manager{
		locker:            &fileLocker{},
		runner:            runner,
		socketWaiter:      waiter,
		qmpDialer:         &fakeQMPDialer{client: qmpClient},
		logger:            log.New(io.Discard, "", 0),
		sshRetryDelay:     0,
		shutdownDelay:     10 * time.Millisecond,
		qmpRetryDelay:     0,
		qmpConnectTimeout: time.Second,
		qmpQuitTimeout:    time.Second,
	}

	err := manager.launch(cancelCtx, manifest, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if got, want := feature.startedAfterSSHSession, 0; got != want {
		t.Fatalf("expected optional feature to start before autoconnect ssh session, got session count %d want %d", got, want)
	}
	if feature.stoppedAt.IsZero() {
		t.Fatal("expected optional feature to stop during teardown")
	}
	if quitAt.Before(feature.stoppedAt) {
		t.Fatalf("expected qmp quit after optional feature stop: quit=%s feature-stop=%s", quitAt, feature.stoppedAt)
	}
}

func (f *fakeOptionalFeature) AppendQEMUArgs(
	qemu manifest.QEMU,
	config *govmmQemu.Config,
	resolveTransport qemuTransportResolver,
	args []string,
) ([]string, error) {
	if f.appendMarker == "" {
		return args, nil
	}
	return append(args, "-device", f.appendMarker), nil
}

func (f *fakeOptionalFeature) StartTask(ctx context.Context, runtime optionalFeatureRuntime, manifest *manifest.Manifest, qmpClient qmpClient) *managedTask {
	if f.runner != nil {
		f.runner.mu.Lock()
		f.startedAfterSSHSession = f.runner.interactiveStarts
		f.runner.mu.Unlock()
	}
	return startManagedTask(ctx, func(taskCtx context.Context) error {
		<-taskCtx.Done()
		f.stoppedAt = time.Now()
		return nil
	})
}

func setOptionalFeaturesForTest(t *testing.T, features ...optionalFeature) {
	t.Helper()

	previous := optionalFeatures
	optionalFeatures = features
	t.Cleanup(func() {
		optionalFeatures = previous
	})
}
