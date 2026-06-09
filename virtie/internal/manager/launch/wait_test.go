package launch

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForEventReturnsWhenProcessDone(t *testing.T) {
	done := make(chan struct{})
	close(done)
	if err := WaitForEvent(context.Background(), EventWait{ProcessDone: done, PollDelay: time.Millisecond}); err != nil {
		t.Fatalf("wait for process done: %v", err)
	}
}

func TestWaitForProcessWaitsForExitStatus(t *testing.T) {
	process := &fakeWaitProcess{done: make(chan struct{})}
	close(process.done)

	if err := WaitForProcess(context.Background(), ProcessWait{
		Stage:     "active session",
		Process:   process,
		PollDelay: time.Millisecond,
	}); err != nil {
		t.Fatalf("wait for process: %v", err)
	}
	if !process.waited {
		t.Fatalf("expected process Wait to be called")
	}
}

func TestWaitForProcessWrapsExitError(t *testing.T) {
	waitErr := errors.New("exit status 1")
	wrappedErr := errors.New("wrapped")
	process := &fakeWaitProcess{name: "ssh", done: make(chan struct{}), err: waitErr}
	close(process.done)

	err := WaitForProcess(context.Background(), ProcessWait{
		Stage:     "active session",
		Process:   process,
		PollDelay: time.Millisecond,
		ProcessError: func(stage string, name string, err error) error {
			if stage != "active session" {
				t.Fatalf("stage: got %q want active session", stage)
			}
			if name != "ssh" {
				t.Fatalf("name: got %q want ssh", name)
			}
			if !errors.Is(err, waitErr) {
				t.Fatalf("process err: got %v want %v", err, waitErr)
			}
			return wrappedErr
		},
	})
	if !errors.Is(err, wrappedErr) {
		t.Fatalf("wrapped err: got %v want %v", err, wrappedErr)
	}
}

func TestWaitForProcessReturnsLifecycleErrorBeforeWait(t *testing.T) {
	lifecycle := NewLifecycle(nil, nil, nil)
	defer lifecycle.Stop()
	wantErr := errors.New("suspended")
	process := &fakeWaitProcess{done: make(chan struct{})}
	lifecycle.Suspend().Request()

	err := WaitForProcess(context.Background(), ProcessWait{
		Stage:     "active session",
		Process:   process,
		Lifecycle: lifecycle,
		PollDelay: time.Millisecond,
		Suspend: func(context.Context) error {
			return wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("suspend err: got %v want %v", err, wantErr)
	}
	if process.waited {
		t.Fatalf("did not expect process Wait after lifecycle error")
	}
}

func TestWaitForEventReturnsWhenDelayCompletes(t *testing.T) {
	if err := WaitForEvent(context.Background(), EventWait{Delay: time.Millisecond, PollDelay: time.Millisecond}); err != nil {
		t.Fatalf("wait for delay: %v", err)
	}
}

type fakeWaitProcess struct {
	name   string
	done   chan struct{}
	err    error
	waited bool
}

func (p *fakeWaitProcess) Done() <-chan struct{} { return p.done }

func (p *fakeWaitProcess) Wait() error {
	p.waited = true
	return p.err
}

func (p *fakeWaitProcess) Name() string { return p.name }

func TestWaitForEventHandlesSuspend(t *testing.T) {
	lifecycle := NewLifecycle(nil, nil, nil)
	defer lifecycle.Stop()
	wantErr := errors.New("suspended")
	called := false
	lifecycle.Suspend().Request()

	err := WaitForEvent(context.Background(), EventWait{
		Lifecycle: lifecycle,
		PollDelay: time.Millisecond,
		Suspend: func(context.Context) error {
			called = true
			return wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("suspend error: got %v want %v", err, wantErr)
	}
	if !called {
		t.Fatal("suspend callback was not called")
	}
}

func TestWaitForEventHandlesInfoAndContinues(t *testing.T) {
	lifecycle := NewLifecycle(nil, nil, nil)
	defer lifecycle.Stop()
	done := make(chan struct{})
	infoCalled := make(chan struct{}, 1)
	lifecycle.RequestInfo()

	go func() {
		<-infoCalled
		close(done)
	}()

	if err := WaitForEvent(context.Background(), EventWait{
		ProcessDone: done,
		Lifecycle:   lifecycle,
		PollDelay:   time.Millisecond,
		Info: func(context.Context) {
			infoCalled <- struct{}{}
		},
	}); err != nil {
		t.Fatalf("wait after info: %v", err)
	}
}

func TestWaitForEventChecksUnexpectedExit(t *testing.T) {
	wantErr := errors.New("unexpected exit")
	err := WaitForEvent(context.Background(), EventWait{
		Stage:     "vm session",
		PollDelay: time.Millisecond,
		Check: func(stage string) error {
			if stage != "vm session" {
				t.Fatalf("stage: got %q want vm session", stage)
			}
			return wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("check error: got %v want %v", err, wantErr)
	}
}

func TestWaitForEventWrapsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wantErr := errors.New("wrapped cancellation")

	err := WaitForEvent(ctx, EventWait{
		Stage:     "active session",
		PollDelay: time.Millisecond,
		Cancel: func(stage string, err error) error {
			if stage != "active session" {
				t.Fatalf("stage: got %q want active session", stage)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel err: got %v want context canceled", err)
			}
			return wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("cancel error: got %v want %v", err, wantErr)
	}
}
