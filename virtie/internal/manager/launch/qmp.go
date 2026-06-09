package launch

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	if err := WaitForAsync(ctx, AsyncWait{
		Stage:     stage,
		PollDelay: wait.PollDelay,
		Wait: func(waitCtx context.Context) error {
			return wait.SocketWaiter.Wait(waitCtx, []string{wait.SocketPath})
		},
		Check:  wait.Check,
		Result: wait.Result,
		Cancel: wait.Cancel,
	}); err != nil {
		return nil, err
	}

	client, err := qmpclient.DialWithRetry(ctx, wait.Dialer, qmpclient.DialRetry{
		SocketPath: wait.SocketPath,
		Timeout:    wait.ConnectTimeout,
		RetryDelay: wait.RetryDelay,
		Check: func() error {
			if wait.Check == nil {
				return nil
			}
			return wait.Check(stage)
		},
	})
	if err != nil {
		if wait.Cancel != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return nil, wait.Cancel(stage, err)
		}
		return nil, err
	}
	return client, nil
}
