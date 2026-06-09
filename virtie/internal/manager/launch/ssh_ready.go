package launch

import (
	"context"
	"fmt"
	"time"

	"github.com/shazow/agentspace/virtie/internal/readiness"
)

const SSHReadyToken = "SSH-READY"

type SSHReadyWait struct {
	Stage        string
	SocketPath   string
	Token        string
	Timeout      time.Duration
	PollDelay    time.Duration
	SocketWaiter SocketWaiter
	Dialer       SSHReadyDialer

	Check func(stage string) error
	Wrap  func(stage string, err error) error
}

func WaitForSSHReady(ctx context.Context, wait SSHReadyWait) error {
	stage := wait.Stage
	if stage == "" {
		stage = "vm startup"
	}
	token := wait.Token
	if token == "" {
		token = SSHReadyToken
	}
	wrap := wait.Wrap
	if wrap == nil {
		wrap = WrapStage
	}

	readyCtx, cancel := context.WithTimeout(ctx, wait.Timeout)
	defer cancel()

	if err := WaitForSockets(readyCtx, SocketWait{
		Stage:        stage,
		SocketPaths:  []string{wait.SocketPath},
		SocketWaiter: wait.SocketWaiter,
		PollDelay:    wait.PollDelay,
		Check:        wait.Check,
		Result:       wrap,
		Cancel:       wrap,
	}); err != nil {
		if readyCtx.Err() != nil {
			return wrapSSHReadyWait(stage, readyCtx.Err(), wrap)
		}
		return err
	}

	if wait.Dialer == nil {
		return wrap(stage, fmt.Errorf("ssh readiness dialer is not configured"))
	}
	reader, err := wait.Dialer.Dial(readyCtx, wait.SocketPath, wait.Timeout)
	if err != nil {
		if readyCtx.Err() != nil {
			return wrapSSHReadyWait(stage, readyCtx.Err(), wrap)
		}
		return wrap(stage, fmt.Errorf("connect ssh readiness socket %q: %w", wait.SocketPath, err))
	}
	defer reader.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- readiness.ReadToken(reader, token)
	}()

	pollDelay := wait.PollDelay
	if pollDelay <= 0 {
		pollDelay = time.Second
	}
	ticker := time.NewTicker(pollDelay)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return wrap(stage, err)
			}
			return nil
		case <-ticker.C:
			if wait.Check != nil {
				if err := wait.Check(stage); err != nil {
					return err
				}
			}
		case <-readyCtx.Done():
			return wrapSSHReadyWait(stage, readyCtx.Err(), wrap)
		}
	}
}

func wrapSSHReadyWait(stage string, err error, wrap func(stage string, err error) error) error {
	return wrap(stage, fmt.Errorf("wait for ssh readiness: %w", err))
}
