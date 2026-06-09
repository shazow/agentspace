package manager

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	govmmQemu "github.com/kata-containers/govmm/qemu"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type fakeOptionalFeature struct {
	appendMarker           string
	runner                 *launchRunner
	startedAfterSSHSession int
	stoppedAt              time.Time
}

func TestBuildQEMUCommandAppendsOptionalFeatureArgs(t *testing.T) {
	feature := &fakeOptionalFeature{appendMarker: "fake-feature-device"}
	setOptionalFeaturesForTest(t, feature)

	spec, err := buildQEMUCommand(validManifest("/tmp/work"), 42, false)
	if err != nil {
		t.Fatalf("build qemu command: %v", err)
	}
	if !containsString(commandArgs(spec), feature.appendMarker) {
		t.Fatalf("expected qemu args to include optional feature marker %q: %v", feature.appendMarker, commandArgs(spec))
	}
}

func TestManagerLaunchStartsOptionalFeatureBeforeSSHSessionAndStopsItBeforeQuit(t *testing.T) {
	tmpDir := t.TempDir()
	manifest := validManifest(tmpDir)
	manifest.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")

	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &launchRunner{
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
		logger:            slog.New(slog.DiscardHandler),
		locker:            &fileLocker{},
		runner:            runner,
		socketWaiter:      waiter,
		qmpDialer:         &fakeQMPDialer{client: qmpClient},
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

func (f *fakeOptionalFeature) StartTask(ctx context.Context, runtime optionalFeatureRuntime, manifest *manifest.Manifest, qmpClient qmpClient) *runtimepkg.Task {
	if f.runner != nil {
		f.runner.mu.Lock()
		f.startedAfterSSHSession = f.runner.interactiveStarts
		f.runner.mu.Unlock()
	}
	return runtimepkg.StartTask(ctx, func(taskCtx context.Context) error {
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
