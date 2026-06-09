package runtime

import "context"

type ForegroundWait func(context.Context) error

func WaitForeground(ctx context.Context, savedSuspend *SavedSuspendState, wait ForegroundWait, savedSuspendExit func(error) bool) error {
	err := wait(ctx)
	if err != nil && savedSuspendExit(err) {
		savedSuspend.MarkSaved()
	}
	return err
}
