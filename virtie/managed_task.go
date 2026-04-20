package virtie

import (
	"context"
	"sync"
)

type managedTask struct {
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
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
