package manager

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

const defaultLaunchSignalTimeout = 5 * time.Second

// Suspend saves the running VM state to disk and exits the launch process.
func Suspend(ctx context.Context, manifest *manifest.Manifest) error {
	return newManager().suspend(ctx, manifest)
}

func (m *manager) suspend(ctx context.Context, manifest *manifest.Manifest) error {
	pid, err := m.launchPID(manifest)
	if err != nil {
		if saved, stateErr := hasSavedSuspendState(manifest); stateErr != nil {
			return &stageError{Stage: "qmp suspend", Err: stateErr}
		} else if saved {
			return nil
		}
		return err
	}

	if err := m.signalLaunchPID(pid, syscall.SIGTSTP); err != nil {
		return &stageError{Stage: "launch signal", Err: err}
	}

	waitCtx, cancel := context.WithTimeout(ctx, m.effectiveSuspendSignalTimeout(manifest))
	defer cancel()

	if err := waitForSavedSuspendState(waitCtx, manifest, m.effectiveSuspendSignalTimeout(manifest)); err != nil {
		return &stageError{Stage: "qmp suspend", Err: err}
	}
	if err := waitForLaunchExited(waitCtx, manifest, m.effectiveSuspendSignalTimeout(manifest)); err != nil {
		return &stageError{Stage: "launch signal", Err: err}
	}
	return nil
}

func (m *manager) launchPID(manifest *manifest.Manifest) (int, error) {
	if err := manifest.Validate(); err != nil {
		return 0, err
	}

	pid, err := readLaunchPID(manifest)
	if err != nil {
		return 0, &stageError{Stage: "launch pid", Err: err}
	}

	signaler := m.effectivePIDSignaler()
	if err := signaler.Exists(pid); err != nil {
		if errorsIsNoProcess(err) {
			return 0, &stageError{Stage: "launch pid", Err: fmt.Errorf("stale launch pid %d from %q: process does not exist", pid, launchPIDPath(manifest))}
		}
		return 0, &stageError{Stage: "launch pid", Err: fmt.Errorf("check launch pid %d from %q: %w", pid, launchPIDPath(manifest), err)}
	}
	if err := validateLaunchLock(manifest, pid); err != nil {
		return 0, &stageError{Stage: "launch pid", Err: err}
	}
	return pid, nil
}

func (m *manager) signalLaunchPID(pid int, sig os.Signal) error {
	if err := m.effectivePIDSignaler().Signal(pid, sig); err != nil {
		if errorsIsNoProcess(err) {
			return fmt.Errorf("stale launch pid %d: process does not exist", pid)
		}
		return fmt.Errorf("signal launch pid %d with %s: %w", pid, sig, err)
	}
	return nil
}

func (m *manager) effectivePIDSignaler() pidSignaler {
	if m.pidSignaler != nil {
		return m.pidSignaler
	}
	return syscallPIDSignaler{}
}

func waitForLaunchExited(ctx context.Context, manifest *manifest.Manifest, timeout time.Duration) error {
	return waitForStateCondition(ctx, timeout, func() (bool, error) {
		_, err := os.Stat(launchPIDPath(manifest))
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}, fmt.Sprintf("launch pid %q was not removed", launchPIDPath(manifest)))
}

func waitForSavedSuspendState(ctx context.Context, manifest *manifest.Manifest, timeout time.Duration) error {
	return waitForStateCondition(ctx, timeout, func() (bool, error) {
		state, err := readSuspendState(manifest)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return state.Status == "saved", nil
	}, fmt.Sprintf("saved suspend state %q was not written", suspendStatePath(manifest)))
}

func waitForStateCondition(ctx context.Context, timeout time.Duration, ready func() (bool, error), timeoutMessage string) error {
	if timeout <= 0 {
		timeout = defaultQMPConnectTimeout
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		ok, err := ready()
		if ok {
			return nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastErr != nil {
				return fmt.Errorf("%s before timeout: %w", timeoutMessage, lastErr)
			}
			return fmt.Errorf("%s before timeout", timeoutMessage)
		case <-ticker.C:
		}
	}
}

func (m *manager) effectiveSuspendSignalTimeout(manifest *manifest.Manifest) time.Duration {
	shutdownDelay := m.shutdownDelay
	if shutdownDelay <= 0 {
		shutdownDelay = defaultShutdownDelay
	}

	teardownProcesses := 2 // qemu plus the active ssh session when present.
	if daemons, err := manifest.ResolvedVirtioFSDaemons(); err == nil {
		teardownProcesses += len(daemons)
	} else {
		teardownProcesses += len(manifest.VirtioFS.Daemons)
	}

	return defaultLaunchSignalTimeout +
		m.effectiveQMPMigrationTimeout() +
		m.effectiveQMPQuitTimeout() +
		time.Duration(teardownProcesses)*shutdownDelay
}
