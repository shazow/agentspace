package virtie

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunQMPMonitorOpReturnsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	unblock := make(chan struct{})
	done := make(chan struct{})
	abortCalls := 0

	errCh := make(chan error, 1)
	go func() {
		errCh <- runQMPMonitorOp(ctx, time.Second, func() error {
			abortCalls++
			close(unblock)
			return nil
		}, func() error {
			close(started)
			<-unblock
			close(done)
			return nil
		})
	}()

	<-started
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runQMPMonitorOp did not return after context cancellation")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked QMP operation was not aborted")
	}

	if abortCalls != 1 {
		t.Fatalf("unexpected abort calls: got %d want 1", abortCalls)
	}
}

func TestRunQMPMonitorOpReturnsOnTimeout(t *testing.T) {
	unblock := make(chan struct{})
	done := make(chan struct{})
	abortCalls := 0

	err := runQMPMonitorOp(context.Background(), 10*time.Millisecond, func() error {
		abortCalls++
		close(unblock)
		return nil
	}, func() error {
		<-unblock
		close(done)
		return nil
	})
	if !errors.Is(err, errQMPTimeout) {
		t.Fatalf("expected qmp timeout, got %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked QMP operation was not aborted after timeout")
	}

	if abortCalls != 1 {
		t.Fatalf("unexpected abort calls: got %d want 1", abortCalls)
	}
}
