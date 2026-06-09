package runtime

import (
	"errors"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

func TestCloserRunsOnceAndTracksStoppedState(t *testing.T) {
	state := NewState(control.RuntimeReady)
	closer := NewCloser(state)
	calls := 0

	if err := closer.Close(func() error {
		calls++
		if got := state.Current(); got != control.RuntimeStopping {
			t.Fatalf("state during close: got %q want %q", got, control.RuntimeStopping)
		}
		return nil
	}); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := closer.Close(func() error {
		calls++
		return errors.New("second close should not run")
	}); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if calls != 1 {
		t.Fatalf("close calls: got %d want 1", calls)
	}
	if got := state.Current(); got != control.RuntimeStopped {
		t.Fatalf("state after close: got %q want %q", got, control.RuntimeStopped)
	}
}

func TestCloserReturnsFirstCloseError(t *testing.T) {
	wantErr := errors.New("close failed")
	closer := NewCloser(NewState(control.RuntimeReady))
	if err := closer.Close(func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("close error: got %v want %v", err, wantErr)
	}
	if err := closer.Close(func() error { return nil }); err != nil {
		t.Fatalf("second close: %v", err)
	}
}
