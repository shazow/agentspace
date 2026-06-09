package launch

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/executor/executortest"
)

func TestWaitForAsyncReturnsWhenWaitCompletes(t *testing.T) {
	if err := WaitForAsync(context.Background(), AsyncWait{
		PollDelay: time.Millisecond,
		Wait: func(context.Context) error {
			return nil
		},
	}); err != nil {
		t.Fatalf("wait async: %v", err)
	}
}

func TestWaitForSocketsUsesSocketWaiter(t *testing.T) {
	waiter := &fakeAsyncSocketWaiter{}
	if err := WaitForSockets(context.Background(), SocketWait{
		Stage:        "virtiofs startup",
		SocketPaths:  []string{"a.sock", "b.sock"},
		SocketWaiter: waiter,
		PollDelay:    time.Millisecond,
	}); err != nil {
		t.Fatalf("wait for sockets: %v", err)
	}
	if got, want := waiter.paths, []string{"a.sock", "b.sock"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("socket paths: got %v want %v", got, want)
	}
}

func TestWaitForSocketsWrapsSocketError(t *testing.T) {
	waitErr := errors.New("socket failed")
	wrappedErr := errors.New("wrapped")
	err := WaitForSockets(context.Background(), SocketWait{
		Stage:        "guest agent",
		SocketWaiter: &fakeAsyncSocketWaiter{err: waitErr},
		PollDelay:    time.Millisecond,
		Result: func(stage string, err error) error {
			if stage != "guest agent" {
				t.Fatalf("stage: got %q want guest agent", stage)
			}
			if !errors.Is(err, waitErr) {
				t.Fatalf("wait err: got %v want %v", err, waitErr)
			}
			return wrappedErr
		},
	})
	if !errors.Is(err, wrappedErr) {
		t.Fatalf("wrapped err: got %v want %v", err, wrappedErr)
	}
}

func TestWaitForSocketsDefaultsToStageWrapping(t *testing.T) {
	waitErr := errors.New("socket failed")
	err := WaitForSockets(context.Background(), SocketWait{
		Stage:        "guest agent",
		SocketWaiter: &fakeAsyncSocketWaiter{err: waitErr},
		PollDelay:    time.Millisecond,
	})
	var stageErr *StageError
	if !errors.As(err, &stageErr) || stageErr.Stage != "guest agent" || !errors.Is(err, waitErr) {
		t.Fatalf("default wrapped err: got %v", err)
	}
}

func TestWaitForSocketsDefaultsToWatcherExitCheck(t *testing.T) {
	process := &executortest.Process{OverrideName: "qemu", Exited: true}
	err := WaitForSockets(context.Background(), SocketWait{
		Stage:     "virtiofs startup",
		Watchers:  executor.NewGroup(process.Process()),
		PollDelay: time.Millisecond,
		SocketWaiter: &fakeAsyncSocketWaiter{
			block: true,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "virtiofs startup: qemu exited unexpectedly") {
		t.Fatalf("watcher exit error: got %v", err)
	}
}

func TestWaitForAsyncWrapsWaitError(t *testing.T) {
	waitErr := errors.New("socket failed")
	wrappedErr := errors.New("wrapped")
	err := WaitForAsync(context.Background(), AsyncWait{
		Stage:     "vm startup",
		PollDelay: time.Millisecond,
		Wait: func(context.Context) error {
			return waitErr
		},
		Result: func(stage string, err error) error {
			if stage != "vm startup" {
				t.Fatalf("stage: got %q want vm startup", stage)
			}
			if !errors.Is(err, waitErr) {
				t.Fatalf("wait err: got %v want %v", err, waitErr)
			}
			return wrappedErr
		},
	})
	if !errors.Is(err, wrappedErr) {
		t.Fatalf("wrapped err: got %v want %v", err, wrappedErr)
	}
}

type fakeAsyncSocketWaiter struct {
	paths []string
	err   error
	block bool
}

func (w *fakeAsyncSocketWaiter) Wait(ctx context.Context, socketPaths []string) error {
	w.paths = append([]string(nil), socketPaths...)
	if w.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return w.err
}

func TestWaitForAsyncChecksWhileWaiting(t *testing.T) {
	checkErr := errors.New("process exited")
	err := WaitForAsync(context.Background(), AsyncWait{
		Stage:     "virtiofs startup",
		PollDelay: time.Millisecond,
		Wait: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		Check: func(stage string) error {
			if stage != "virtiofs startup" {
				t.Fatalf("stage: got %q want virtiofs startup", stage)
			}
			return checkErr
		},
	})
	if !errors.Is(err, checkErr) {
		t.Fatalf("check err: got %v want %v", err, checkErr)
	}
}

func TestWaitForAsyncWrapsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wrappedErr := errors.New("cancel wrapped")
	err := WaitForAsync(ctx, AsyncWait{
		Stage:     "guest agent",
		PollDelay: time.Millisecond,
		Wait: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		Cancel: func(stage string, err error) error {
			if stage != "guest agent" {
				t.Fatalf("stage: got %q want guest agent", stage)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel err: got %v want context canceled", err)
			}
			return wrappedErr
		},
	})
	if !errors.Is(err, wrappedErr) {
		t.Fatalf("cancel err: got %v want %v", err, wrappedErr)
	}
}
