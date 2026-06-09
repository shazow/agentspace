package runtime

import (
	"context"
	"errors"
	"testing"
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
