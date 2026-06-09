package launch

import (
	"context"
	"errors"
	"testing"
	"time"
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
