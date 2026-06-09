package launch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/qga"
)

func TestWaitForGuestAgentWaitsForSocketThenDials(t *testing.T) {
	waiter := &fakeGuestAgentSocketWaiter{}
	client := &fakeGuestAgentClient{}
	dialer := &fakeGuestAgentDialer{client: client}

	got, err := WaitForGuestAgent(context.Background(), GuestAgentWait{
		SocketPath:     "qga.sock",
		SocketWaiter:   waiter,
		Dialer:         dialer,
		ConnectTimeout: 10 * time.Millisecond,
		CommandTimeout: 20 * time.Millisecond,
		RetryDelay:     time.Millisecond,
		PollDelay:      time.Millisecond,
	})
	if err != nil {
		t.Fatalf("wait for guest agent: %v", err)
	}
	if got != client {
		t.Fatalf("client: got %#v want %#v", got, client)
	}
	if !waiter.called {
		t.Fatal("expected socket waiter to be called")
	}
	if got, want := dialer.socketPath, "qga.sock"; got != want {
		t.Fatalf("dial socket: got %q want %q", got, want)
	}
	if got, want := dialer.timeout, 10*time.Millisecond; got != want {
		t.Fatalf("dial timeout: got %s want %s", got, want)
	}
	if got, want := client.pingTimeout, 20*time.Millisecond; got != want {
		t.Fatalf("ping timeout: got %s want %s", got, want)
	}
}

func TestWaitForGuestAgentWrapsSocketWaitError(t *testing.T) {
	waitErr := errors.New("socket failed")
	wrappedErr := errors.New("wrapped")
	_, err := WaitForGuestAgent(context.Background(), GuestAgentWait{
		Stage:        "guest agent",
		SocketWaiter: &fakeGuestAgentSocketWaiter{err: waitErr},
		Dialer:       &fakeGuestAgentDialer{client: &fakeGuestAgentClient{}},
		PollDelay:    time.Millisecond,
		Result: func(stage string, err error) error {
			if stage != "guest agent" {
				t.Fatalf("stage: got %q want guest agent", stage)
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

func TestWaitForGuestAgentChecksWhileDialing(t *testing.T) {
	checkErr := errors.New("qemu exited")
	dialer := &fakeGuestAgentDialer{err: errors.New("not ready")}
	_, err := WaitForGuestAgent(context.Background(), GuestAgentWait{
		SocketWaiter: &fakeGuestAgentSocketWaiter{},
		Dialer:       dialer,
		RetryDelay:   time.Millisecond,
		PollDelay:    time.Millisecond,
		Check: func(stage string) error {
			if stage != "guest agent" {
				t.Fatalf("stage: got %q want guest agent", stage)
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

func TestWaitForGuestAgentWrapsDialCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelErr := errors.New("cancel wrapped")
	_, err := WaitForGuestAgent(ctx, GuestAgentWait{
		SocketWaiter: &fakeGuestAgentSocketWaiter{},
		Dialer:       &fakeGuestAgentDialer{client: &fakeGuestAgentClient{}},
		RetryDelay:   time.Millisecond,
		PollDelay:    time.Millisecond,
		Cancel: func(stage string, err error) error {
			if stage != "guest agent" {
				t.Fatalf("stage: got %q want guest agent", stage)
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

func TestWaitForGuestAgentDefaultsToStageWrappingDialCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := WaitForGuestAgent(ctx, GuestAgentWait{
		SocketWaiter: &fakeGuestAgentSocketWaiter{},
		Dialer:       &fakeGuestAgentDialer{client: &fakeGuestAgentClient{}},
		RetryDelay:   time.Millisecond,
		PollDelay:    time.Millisecond,
	})
	var stageErr *StageError
	if !errors.As(err, &stageErr) || stageErr.Stage != "guest agent" || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err: got %v", err)
	}
}

type fakeGuestAgentSocketWaiter struct {
	called bool
	paths  []string
	err    error
}

func (w *fakeGuestAgentSocketWaiter) Wait(ctx context.Context, socketPaths []string) error {
	w.called = true
	w.paths = append([]string(nil), socketPaths...)
	return w.err
}

type fakeGuestAgentDialer struct {
	calls      int
	socketPath string
	timeout    time.Duration
	client     qga.Client
	err        error
}

func (d *fakeGuestAgentDialer) Dial(ctx context.Context, socketPath string, timeout time.Duration) (qga.Client, error) {
	d.calls++
	d.socketPath = socketPath
	d.timeout = timeout
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if d.err != nil {
		return nil, d.err
	}
	return d.client, nil
}

type fakeGuestAgentClient struct {
	pingTimeout time.Duration
}

func (c *fakeGuestAgentClient) Ping(timeout time.Duration) error {
	c.pingTimeout = timeout
	return nil
}

func (c *fakeGuestAgentClient) OpenFile(time.Duration, string) (int, error) { return 0, nil }
func (c *fakeGuestAgentClient) OpenFileRead(time.Duration, string) (int, error) {
	return 0, nil
}
func (c *fakeGuestAgentClient) ReadFile(time.Duration, int, int) (string, bool, error) {
	return "", false, nil
}
func (c *fakeGuestAgentClient) WriteFile(time.Duration, int, string) error { return nil }
func (c *fakeGuestAgentClient) CloseFile(time.Duration, int) error         { return nil }
func (c *fakeGuestAgentClient) Exec(time.Duration, string, []string, bool) (int, error) {
	return 0, nil
}
func (c *fakeGuestAgentClient) ExecStatus(time.Duration, int) (qga.ExecStatus, error) {
	return qga.ExecStatus{}, nil
}
func (c *fakeGuestAgentClient) Disconnect() error { return nil }
