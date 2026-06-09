package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type fakeSuspendRequester struct {
	err error
}

func (r fakeSuspendRequester) RequestAndWait(context.Context) error {
	return r.err
}

func TestMarkReadyAndStatus(t *testing.T) {
	state := NewState(control.RuntimeStarting)
	MarkReady(state)
	stats := NewStats(time.Now())
	stats.MarkBootStarted(time.Now().Add(time.Second))
	got := Status(state, 7, control.StatusPaths{ControlSocket: "/tmp/virtie.sock"}, stats)
	if got.State != control.RuntimeReady || got.CID != 7 || got.Paths.ControlSocket != "/tmp/virtie.sock" {
		t.Fatalf("status: %#v", got)
	}
	if got.Stats.StartedToBoot == "" {
		t.Fatalf("expected stats conversion in status: %#v", got.Stats)
	}
}

func TestQueueSuspendTransitionsState(t *testing.T) {
	state := NewState(control.RuntimeReady)
	if err := QueueSuspend(context.Background(), state, fakeSuspendRequester{}, func(error) bool { return false }); err != nil {
		t.Fatalf("queue suspend: %v", err)
	}
	if got := state.Current(); got != control.RuntimeSuspended {
		t.Fatalf("state: got %s want %s", got, control.RuntimeSuspended)
	}
}

func TestQueueSuspendAllowsSavedSuspendExit(t *testing.T) {
	savedErr := errors.New("saved suspend")
	state := NewState(control.RuntimeReady)
	err := QueueSuspend(context.Background(), state, fakeSuspendRequester{err: savedErr}, func(err error) bool {
		return errors.Is(err, savedErr)
	})
	if err != nil {
		t.Fatalf("queue suspend: %v", err)
	}
	if got := state.Current(); got != control.RuntimeSuspended {
		t.Fatalf("state: got %s want %s", got, control.RuntimeSuspended)
	}
}

func TestQueueSuspendPropagatesUnexpectedError(t *testing.T) {
	suspendErr := errors.New("failed")
	state := NewState(control.RuntimeReady)
	err := QueueSuspend(context.Background(), state, fakeSuspendRequester{err: suspendErr}, func(error) bool {
		return false
	})
	if !errors.Is(err, suspendErr) {
		t.Fatalf("error: got %v want %v", err, suspendErr)
	}
	if got := state.Current(); got != control.RuntimeSuspending {
		t.Fatalf("state: got %s want %s", got, control.RuntimeSuspending)
	}
}

func TestSuspendReturnsSavedResponse(t *testing.T) {
	state := NewState(control.RuntimeReady)
	resp, err := Suspend(context.Background(), SuspendOperation{
		State:       state,
		Requester:   fakeSuspendRequester{},
		VMStatePath: "/tmp/vmstate",
	})
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if !resp.Saved || resp.VMStatePath != "/tmp/vmstate" {
		t.Fatalf("response: %#v", resp)
	}
}

func TestSuspendReturnsNotReadyError(t *testing.T) {
	_, err := Suspend(context.Background(), SuspendOperation{State: NewState(control.RuntimeReady)})
	if !errors.Is(err, ErrSuspendNotReady) {
		t.Fatalf("error: got %v want %v", err, ErrSuspendNotReady)
	}
}
