package virtie

import (
	"context"
	"errors"
	"sync"
)

type managedTask struct {
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
}

type managedTaskGroup struct {
	tasks []*managedTask
}

func startManagedTask(ctx context.Context, fn func(context.Context) error) *managedTask {
	taskCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() {
		done <- fn(taskCtx)
		close(done)
	}()

	return &managedTask{
		cancel: cancel,
		done:   done,
	}
}

func (t *managedTask) Stop() error {
	if t == nil {
		return nil
	}

	t.once.Do(func() {
		t.cancel()
		t.err = <-t.done
	})
	return t.err
}

func (g *managedTaskGroup) Add(task *managedTask) {
	if g == nil || task == nil {
		return
	}
	g.tasks = append(g.tasks, task)
}

func (g *managedTaskGroup) Stop() error {
	if g == nil {
		return nil
	}

	errs := make([]error, 0, len(g.tasks))
	for i := len(g.tasks) - 1; i >= 0; i-- {
		errs = append(errs, g.tasks[i].Stop())
	}
	return errors.Join(errs...)
}
