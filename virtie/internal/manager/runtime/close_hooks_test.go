package runtime

import (
	"context"
	"testing"
)

func TestNewCloseHooksGatesWriteBack(t *testing.T) {
	state := NewWriteBackState()
	called := false
	hooks := NewCloseHooks(CloseHookActions{
		WriteBackState: state,
		WriteBack: func(context.Context) error {
			called = true
			return nil
		},
	})
	if err := hooks.WriteBack(context.Background()); err != nil {
		t.Fatalf("write back: %v", err)
	}
	if called {
		t.Fatal("write-back ran while disabled")
	}

	state.Enable()
	if err := hooks.WriteBack(context.Background()); err != nil {
		t.Fatalf("write back: %v", err)
	}
	if !called {
		t.Fatal("write-back did not run after enabling state")
	}
}

func TestNewCloseHooksKeepsCleanupAndStats(t *testing.T) {
	var cleanupCalled bool
	var statsCalled bool
	hooks := NewCloseHooks(CloseHookActions{
		Cleanup: func() error {
			cleanupCalled = true
			return nil
		},
		Stats: func() {
			statsCalled = true
		},
	})
	if err := hooks.Cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	hooks.Stats()
	if !cleanupCalled || !statsCalled {
		t.Fatalf("hooks did not run: cleanup=%v stats=%v", cleanupCalled, statsCalled)
	}
}
