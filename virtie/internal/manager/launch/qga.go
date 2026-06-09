package launch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
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
	Watchers       executor.Group

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
	if err := WaitForSockets(ctx, SocketWait{
		Stage:        stage,
		SocketPaths:  []string{wait.SocketPath},
		SocketWaiter: wait.SocketWaiter,
		PollDelay:    wait.PollDelay,
		Check:        check,
		Result:       result,
		Cancel:       cancel,
	}); err != nil {
		return nil, err
	}

	client, err := qga.DialWithRetry(ctx, wait.Dialer, qga.DialRetry{
		SocketPath:     wait.SocketPath,
		ConnectTimeout: wait.ConnectTimeout,
		CommandTimeout: wait.CommandTimeout,
		RetryDelay:     wait.RetryDelay,
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
