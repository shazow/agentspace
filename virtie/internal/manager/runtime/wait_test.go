package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

func TestWaitForegroundMarksSavedSuspend(t *testing.T) {
	savedErr := errors.New("saved suspend")
	state := NewSavedSuspendState()
	err := WaitForeground(context.Background(), state, func(context.Context) error {
		return savedErr
	}, func(err error) bool {
		return errors.Is(err, savedErr)
	})
	if !errors.Is(err, savedErr) {
		t.Fatalf("error: got %v want %v", err, savedErr)
	}
	if !state.Saved() {
		t.Fatal("saved suspend state was not marked")
	}
}

func TestWaitForegroundLeavesStateUnmarkedForOtherErrors(t *testing.T) {
	waitErr := errors.New("wait failed")
	state := NewSavedSuspendState()
	err := WaitForeground(context.Background(), state, func(context.Context) error {
		return waitErr
	}, func(error) bool {
		return false
	})
	if !errors.Is(err, waitErr) {
		t.Fatalf("error: got %v want %v", err, waitErr)
	}
	if state.Saved() {
		t.Fatal("saved suspend state was marked for unrelated error")
	}
}

func TestControlWaitForegroundMapsMissingWaitToFailedPrecondition(t *testing.T) {
	err := ControlWaitForeground(context.Background(), ForegroundWaitOperation{})
	var rpcErr *control.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error type: got %T", err)
	}
	if rpcErr.Code != control.ErrFailedPrecondition {
		t.Fatalf("code: got %s want %s", rpcErr.Code, control.ErrFailedPrecondition)
	}
}

func TestControlWaitForegroundRunsConfiguredWait(t *testing.T) {
	var called bool
	err := ControlWaitForeground(context.Background(), ForegroundWaitOperation{
		SavedSuspend: NewSavedSuspendState(),
		Wait: func(context.Context) error {
			called = true
			return nil
		},
		SavedSuspendExit: func(error) bool {
			return false
		},
	})
	if err != nil {
		t.Fatalf("control wait foreground: %v", err)
	}
	if !called {
		t.Fatal("wait callback was not called")
	}
}
