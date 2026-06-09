package launch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

func TestWaitForQMPWaitsForSocketThenDials(t *testing.T) {
	waiter := &fakeQMPSocketWaiter{}
	dialer := &fakeQMPDialer{}

	client, err := WaitForQMP(context.Background(), QMPWait{
		SocketPath:     "qmp.sock",
		SocketWaiter:   waiter,
		Dialer:         dialer,
		ConnectTimeout: 10 * time.Millisecond,
		RetryDelay:     time.Millisecond,
		PollDelay:      time.Millisecond,
	})
	if err != nil {
		t.Fatalf("wait for qmp: %v", err)
	}
	if client != nil {
		t.Fatalf("expected nil fake client, got %#v", client)
	}
	if !waiter.called {
		t.Fatalf("expected socket waiter to be called")
	}
	if got, want := waiter.paths, []string{"qmp.sock"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("socket paths: got %v want %v", got, want)
	}
	if got, want := dialer.socketPath, "qmp.sock"; got != want {
		t.Fatalf("dial socket path: got %q want %q", got, want)
	}
	if got, want := dialer.timeout, 10*time.Millisecond; got != want {
		t.Fatalf("dial timeout: got %s want %s", got, want)
	}
}

func TestWaitForQMPWrapsSocketWaitError(t *testing.T) {
	waitErr := errors.New("socket failed")
	wrappedErr := errors.New("wrapped")
	_, err := WaitForQMP(context.Background(), QMPWait{
		Stage:        "vm startup",
		SocketPath:   "qmp.sock",
		SocketWaiter: &fakeQMPSocketWaiter{err: waitErr},
		Dialer:       &fakeQMPDialer{},
		PollDelay:    time.Millisecond,
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

func TestWaitForQMPChecksWhileDialing(t *testing.T) {
	checkErr := errors.New("qemu exited")
	dialer := &fakeQMPDialer{err: errors.New("not ready")}
	_, err := WaitForQMP(context.Background(), QMPWait{
		SocketPath:   "qmp.sock",
		SocketWaiter: &fakeQMPSocketWaiter{},
		Dialer:       dialer,
		RetryDelay:   time.Millisecond,
		PollDelay:    time.Millisecond,
		Check: func(stage string) error {
			if stage != "vm startup" {
				t.Fatalf("stage: got %q want vm startup", stage)
			}
			if dialer.calls < 2 {
				return nil
			}
			return checkErr
		},
	})
	if !errors.Is(err, checkErr) {
		t.Fatalf("check err: got %v want %v", err, checkErr)
	}
}

func TestWaitForQMPWrapsDialCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelErr := errors.New("cancel wrapped")
	_, err := WaitForQMP(ctx, QMPWait{
		SocketPath:   "qmp.sock",
		SocketWaiter: &fakeQMPSocketWaiter{},
		Dialer:       &fakeQMPDialer{},
		RetryDelay:   time.Millisecond,
		PollDelay:    time.Millisecond,
		Cancel: func(stage string, err error) error {
			if stage != "vm startup" {
				t.Fatalf("stage: got %q want vm startup", stage)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel err: got %v want context canceled", err)
			}
			return cancelErr
		},
	})
	if !errors.Is(err, cancelErr) {
		t.Fatalf("cancel err: got %v want %v", err, cancelErr)
	}
}

type fakeQMPSocketWaiter struct {
	called bool
	paths  []string
	err    error
}

func (w *fakeQMPSocketWaiter) Wait(ctx context.Context, socketPaths []string) error {
	w.called = true
	w.paths = append([]string(nil), socketPaths...)
	return w.err
}

type fakeQMPDialer struct {
	calls      int
	socketPath string
	timeout    time.Duration
	err        error
}

func (d *fakeQMPDialer) Dial(ctx context.Context, socketPath string, timeout time.Duration) (qmpclient.Client, error) {
	d.calls++
	d.socketPath = socketPath
	d.timeout = timeout
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, d.err
}
