package launch

import (
	"context"
	"time"
)

type EventWait struct {
	Stage       string
	ProcessDone <-chan struct{}
	Delay       time.Duration
	Lifecycle   *Lifecycle
	PollDelay   time.Duration

	Suspend func(context.Context) error
	Info    func(context.Context)
	Check   func(stage string) error
	Cancel  func(stage string, err error) error
}

func WaitForEvent(ctx context.Context, wait EventWait) error {
	if wait.PollDelay <= 0 {
		wait.PollDelay = time.Second
	}
	var delayDone <-chan time.Time
	var timer *time.Timer
	if wait.Delay > 0 {
		timer = time.NewTimer(wait.Delay)
		delayDone = timer.C
		defer timer.Stop()
	}

	ticker := time.NewTicker(wait.PollDelay)
	defer ticker.Stop()

	for {
		select {
		case <-wait.ProcessDone:
			return nil
		case <-delayDone:
			return nil
		case <-suspendNotify(wait.Lifecycle):
			if wait.Suspend != nil {
				return wait.Suspend(ctx)
			}
		case <-infoNotify(wait.Lifecycle):
			if wait.Info != nil {
				wait.Info(ctx)
			}
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

func suspendNotify(lifecycle *Lifecycle) <-chan struct{} {
	if lifecycle == nil || lifecycle.Suspend() == nil {
		return nil
	}
	return lifecycle.Suspend().Notify()
}

func infoNotify(lifecycle *Lifecycle) <-chan struct{} {
	if lifecycle == nil {
		return nil
	}
	return lifecycle.Info()
}
