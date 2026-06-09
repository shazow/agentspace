package runtime

import (
	"context"
	"errors"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

var ErrForegroundWaitNotConfigured = errors.New("runtime foreground wait is not configured")

type ForegroundWait func(context.Context) error

type ForegroundWaitOperation struct {
	SavedSuspend     *SavedSuspendState
	Wait             ForegroundWait
	SavedSuspendExit func(error) bool
}

func WaitForeground(ctx context.Context, savedSuspend *SavedSuspendState, wait ForegroundWait, savedSuspendExit func(error) bool) error {
	err := wait(ctx)
	if err != nil && savedSuspendExit(err) {
		savedSuspend.MarkSaved()
	}
	return err
}

func ControlWaitForeground(ctx context.Context, op ForegroundWaitOperation) error {
	if op.Wait == nil {
		return control.FailedPrecondition(ErrForegroundWaitNotConfigured)
	}
	return WaitForeground(ctx, op.SavedSuspend, op.Wait, op.SavedSuspendExit)
}
