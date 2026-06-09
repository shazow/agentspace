package launch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type QMPWait struct {
	Stage          string
	SocketPath     string
	SocketWaiter   SocketWaiter
	Dialer         qmpclient.Dialer
	ConnectTimeout time.Duration
	RetryDelay     time.Duration
	PollDelay      time.Duration
	Watchers       executor.Group

	Check  func(stage string) error
	Result func(stage string, err error) error
	Cancel func(stage string, err error) error
}

func WaitForQMP(ctx context.Context, wait QMPWait) (qmpclient.Client, error) {
	stage := wait.Stage
	if stage == "" {
		stage = "vm startup"
	}
	if wait.SocketWaiter == nil {
		return nil, fmt.Errorf("qmp socket waiter is not configured")
	}
	check := wait.Check
	if check == nil {
		check = func(stage string) error {
			return FirstUnexpectedExit(stage, wait.Watchers)
		}
	}
	result := wait.Result
	if result == nil {
		result = WrapStage
	}
	cancel := wait.Cancel
	if cancel == nil {
		cancel = WrapStage
	}
	if err := WaitForAsync(ctx, AsyncWait{
		Stage:     stage,
		PollDelay: wait.PollDelay,
		Wait: func(waitCtx context.Context) error {
			return wait.SocketWaiter.Wait(waitCtx, []string{wait.SocketPath})
		},
		Check:  check,
		Result: result,
		Cancel: cancel,
	}); err != nil {
		return nil, err
	}

	client, err := qmpclient.DialWithRetry(ctx, wait.Dialer, qmpclient.DialRetry{
		SocketPath: wait.SocketPath,
		Timeout:    wait.ConnectTimeout,
		RetryDelay: wait.RetryDelay,
		Check: func() error {
			return check(stage)
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, cancel(stage, err)
		}
		return nil, err
	}
	return client, nil
}
