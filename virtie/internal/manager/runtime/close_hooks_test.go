package runtime

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestNewCloseHooksSkipsWriteBackWithoutState(t *testing.T) {
	hooks := NewCloseHooks(CloseHookActions{
		WriteBack: func(context.Context) error {
			t.Fatal("write-back should not run without state")
			return nil
		},
	})
	if err := hooks.WriteBack(context.Background()); err != nil {
		t.Fatalf("write back: %v", err)
	}
}

func TestConfiguredCloseHooksJoinsCleanupAndFinalizesStats(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	var output bytes.Buffer
	stats := NewStats(time.Now().Add(-time.Second))
	var cleanupCalls int

	hooks := ConfiguredCloseHooks(CloseHookConfig{
		Cleanup: []func() error{
			func() error {
				cleanupCalls++
				return cleanupErr
			},
			func() error {
				cleanupCalls++
				return nil
			},
		},
		Stats:       stats,
		StatsOutput: &output,
	})

	if err := hooks.Cleanup(); !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanup error: got %v want %v", err, cleanupErr)
	}
	if cleanupCalls != 2 {
		t.Fatalf("cleanup calls: got %d want 2", cleanupCalls)
	}
	hooks.Stats()
	if ControlStats(stats).CompletedAt.IsZero() {
		t.Fatal("stats were not finalized")
	}
	if !strings.Contains(output.String(), "total=") {
		t.Fatalf("stats output missing runtime: %q", output.String())
	}
}
