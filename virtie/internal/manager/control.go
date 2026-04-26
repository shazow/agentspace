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

// Suspend pauses the running launch process for the manifest through SIGTSTP.
func Suspend(ctx context.Context, manifest *manifest.Manifest) error {
	return newManager().suspend(ctx, manifest)
}

// Resume continues the running launch process for the manifest through SIGCONT.
func Resume(ctx context.Context, manifest *manifest.Manifest) error {
	return newManager().resume(ctx, manifest)
}

func (m *manager) suspend(ctx context.Context, manifest *manifest.Manifest) error {
	pid, err := m.launchPID(manifest)
	if err != nil {
		return err
	}
	if suspended, err := hasPausedSuspendState(manifest); err != nil {
		return &stageError{Stage: "launch signal", Err: err}
	} else if suspended {
		return nil
	}
	if err := m.signalLaunchPID(pid, syscall.SIGTSTP); err != nil {
		return &stageError{Stage: "launch signal", Err: err}
	}
	if err := waitForSuspendPaused(ctx, manifest, defaultLaunchSignalTimeout); err != nil {
		return &stageError{Stage: "launch signal", Err: err}
	}
	return nil
}

func (m *manager) resume(ctx context.Context, manifest *manifest.Manifest) error {
	pid, err := m.launchPID(manifest)
	if err != nil {
		return err
	}
	if err := m.signalLaunchPID(pid, syscall.SIGCONT); err != nil {
		return &stageError{Stage: "launch signal", Err: err}
	}
	if err := waitForSuspendRemoved(ctx, manifest, defaultLaunchSignalTimeout); err != nil {
		return &stageError{Stage: "launch signal", Err: err}
	}
	return nil
}

func (m *manager) suspendConnected(manifest *manifest.Manifest, qmpSocketPath string, client qmpClient) error {
	timeout := m.effectiveQMPCommandTimeout()

	status, err := client.QueryStatus(timeout)
	if err != nil {
		return &stageError{Stage: "qmp suspend", Err: err}
	}

	switch status {
	case "paused":
		return writeSuspendState(manifest, qmpSocketPath, status)
	case "running":
		if err := client.Stop(timeout); err != nil {
			return &stageError{Stage: "qmp suspend", Err: err}
		}
		return writeSuspendState(manifest, qmpSocketPath, "paused")
	default:
		return &stageError{Stage: "qmp suspend", Err: fmt.Errorf("cannot suspend VM while QMP status is %q", status)}
	}
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

func waitForSuspendPaused(ctx context.Context, manifest *manifest.Manifest, timeout time.Duration) error {
	return waitForStateCondition(ctx, timeout, func() (bool, error) {
		state, err := readSuspendState(manifest)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		return state.Status == "paused", nil
	}, fmt.Sprintf("suspend state %q did not report paused", suspendStatePath(manifest)))
}

func waitForSuspendRemoved(ctx context.Context, manifest *manifest.Manifest, timeout time.Duration) error {
	return waitForStateCondition(ctx, timeout, func() (bool, error) {
		_, err := os.Stat(suspendStatePath(manifest))
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}, fmt.Sprintf("suspend state %q was not removed", suspendStatePath(manifest)))
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

func (m *manager) resumeConnected(manifest *manifest.Manifest, client qmpClient) error {
	timeout := m.effectiveQMPCommandTimeout()

	status, err := client.QueryStatus(timeout)
	if err != nil {
		return &stageError{Stage: "qmp resume", Err: err}
	}

	switch status {
	case "paused":
		if err := client.Cont(timeout); err != nil {
			return &stageError{Stage: "qmp resume", Err: err}
		}
		return removeSuspendState(manifest)
	case "running":
		return removeSuspendState(manifest)
	default:
		return &stageError{Stage: "qmp resume", Err: fmt.Errorf("cannot resume VM while QMP status is %q", status)}
	}
}
