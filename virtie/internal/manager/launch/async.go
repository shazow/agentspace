package launch

import (
	"context"
	"time"
)

type AsyncWait struct {
	Stage     string
	PollDelay time.Duration

	Wait   func(context.Context) error
	Check  func(stage string) error
	Result func(stage string, err error) error
	Cancel func(stage string, err error) error
}

type SocketWait struct {
	Stage        string
	SocketPaths  []string
	SocketWaiter SocketWaiter
	PollDelay    time.Duration

	Check  func(stage string) error
	Result func(stage string, err error) error
	Cancel func(stage string, err error) error
}

func WaitForSockets(ctx context.Context, wait SocketWait) error {
	return WaitForAsync(ctx, AsyncWait{
		Stage:     wait.Stage,
		PollDelay: wait.PollDelay,
		Wait: func(waitCtx context.Context) error {
			if wait.SocketWaiter == nil {
				return nil
			}
			return wait.SocketWaiter.Wait(waitCtx, wait.SocketPaths)
		},
		Check:  wait.Check,
		Result: wait.Result,
		Cancel: wait.Cancel,
	})
}

func WaitForAsync(ctx context.Context, wait AsyncWait) error {
	if wait.PollDelay <= 0 {
		wait.PollDelay = time.Second
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		if wait.Wait == nil {
			errCh <- nil
			return
		}
		errCh <- wait.Wait(waitCtx)
	}()

	ticker := time.NewTicker(wait.PollDelay)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil && wait.Result != nil {
				return wait.Result(wait.Stage, err)
			}
			return err
		case <-ticker.C:
			if wait.Check != nil {
				if err := wait.Check(wait.Stage); err != nil {
					return err
				}
			}
		case <-ctx.Done():
			if wait.Cancel != nil {
				return wait.Cancel(wait.Stage, ctx.Err())
			}
			return ctx.Err()
		}
	}
}
