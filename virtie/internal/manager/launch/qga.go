package launch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shazow/agentspace/virtie/internal/qga"
)

type GuestAgentWait struct {
	Stage          string
	SocketPath     string
	SocketWaiter   SocketWaiter
	Dialer         qga.Dialer
	ConnectTimeout time.Duration
	CommandTimeout time.Duration
	RetryDelay     time.Duration
	PollDelay      time.Duration

	Check  func(stage string) error
	Result func(stage string, err error) error
	Cancel func(stage string, err error) error
}

func WaitForGuestAgent(ctx context.Context, wait GuestAgentWait) (qga.Client, error) {
	stage := wait.Stage
	if stage == "" {
		stage = "guest agent"
	}
	if wait.SocketWaiter == nil {
		return nil, fmt.Errorf("guest agent socket waiter is not configured")
	}
	if err := WaitForSockets(ctx, SocketWait{
		Stage:        stage,
		SocketPaths:  []string{wait.SocketPath},
		SocketWaiter: wait.SocketWaiter,
		PollDelay:    wait.PollDelay,
		Check:        wait.Check,
		Result:       wait.Result,
		Cancel:       wait.Cancel,
	}); err != nil {
		return nil, err
	}

	client, err := qga.DialWithRetry(ctx, wait.Dialer, qga.DialRetry{
		SocketPath:     wait.SocketPath,
		ConnectTimeout: wait.ConnectTimeout,
		CommandTimeout: wait.CommandTimeout,
		RetryDelay:     wait.RetryDelay,
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
