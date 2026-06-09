package launch

import (
	"context"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
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

type WaitProcess interface {
	Done() <-chan struct{}
	Wait() error
	Name() string
}

type ProcessWait struct {
	Stage     string
	Process   WaitProcess
	Delay     time.Duration
	Lifecycle *Lifecycle
	PollDelay time.Duration

	Suspend      func(context.Context) error
	Info         func(context.Context)
	Check        func(stage string) error
	Cancel       func(stage string, err error) error
	ProcessError func(stage string, name string, err error) error
}

type LifecycleProcessWait struct {
	Stage     string
	Process   WaitProcess
	Delay     time.Duration
	Lifecycle *Lifecycle
	Watchers  executor.Group
	PollDelay time.Duration

	Suspend func(context.Context) error
	Info    func(context.Context)
}

func WaitForLifecycleProcess(ctx context.Context, wait LifecycleProcessWait) error {
	return WaitForProcess(ctx, ProcessWait{
		Stage:     wait.Stage,
		Process:   wait.Process,
		Delay:     wait.Delay,
		Lifecycle: wait.Lifecycle,
		PollDelay: wait.PollDelay,
		Suspend:   wait.Suspend,
		Info:      wait.Info,
		Check: func(stage string) error {
			return FirstUnexpectedExit(stage, wait.Watchers)
		},
		Cancel:       WrapStage,
		ProcessError: WrapCommandError,
	})
}

func WaitForProcess(ctx context.Context, wait ProcessWait) error {
	var processDone <-chan struct{}
	if wait.Process != nil {
		processDone = wait.Process.Done()
	}
	if err := WaitForEvent(ctx, EventWait{
		Stage:       wait.Stage,
		ProcessDone: processDone,
		Delay:       wait.Delay,
		Lifecycle:   wait.Lifecycle,
		PollDelay:   wait.PollDelay,
		Suspend:     wait.Suspend,
		Info:        wait.Info,
		Check:       wait.Check,
		Cancel:      wait.Cancel,
	}); err != nil {
		return err
	}
	if wait.Process == nil {
		return nil
	}
	if err := wait.Process.Wait(); err != nil {
		if wait.ProcessError != nil {
			return wait.ProcessError(wait.Stage, wait.Process.Name(), err)
		}
		return err
	}
	return nil
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
