package manager

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/readiness"
)

const (
	defaultSSHReadyTimeout = 2 * time.Minute
	sshReadyTimeoutEnv     = "VIRTIE_SSH_READY_TIMEOUT"
	SSHReadyToken          = "SSH-READY"
)

type unixSSHReadyDialer struct{}

func configuredSSHReadyTimeout() time.Duration {
	return readiness.TimeoutFromEnv(sshReadyTimeoutEnv, defaultSSHReadyTimeout)
}

func (d *unixSSHReadyDialer) Dial(ctx context.Context, socketPath string, timeout time.Duration) (io.ReadCloser, error) {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func (m *manager) waitForSSHReady(ctx context.Context, socketPath string, watchers executor.Group) error {
	timeout := m.effectiveSSHReadyTimeout()
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := m.waitForSocketWithStage(readyCtx, "vm startup", socketPath, watchers); err != nil {
		if readyCtx.Err() != nil {
			return &stageError{Stage: "vm startup", Err: fmt.Errorf("wait for ssh readiness: %w", readyCtx.Err())}
		}
		return err
	}

	dialer := m.sshReadyDialer
	if dialer == nil {
		dialer = &unixSSHReadyDialer{}
	}

	reader, err := dialer.Dial(readyCtx, socketPath, timeout)
	if err != nil {
		if readyCtx.Err() != nil {
			return &stageError{Stage: "vm startup", Err: fmt.Errorf("wait for ssh readiness: %w", readyCtx.Err())}
		}
		return &stageError{Stage: "vm startup", Err: fmt.Errorf("connect ssh readiness socket %q: %w", socketPath, err)}
	}
	defer reader.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- readiness.ReadToken(reader, SSHReadyToken)
	}()

	ticker := time.NewTicker(defaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return &stageError{Stage: "vm startup", Err: err}
			}
			return nil
		case <-ticker.C:
			if err := firstUnexpectedExit("vm startup", watchers); err != nil {
				return err
			}
		case <-readyCtx.Done():
			return &stageError{Stage: "vm startup", Err: fmt.Errorf("wait for ssh readiness: %w", readyCtx.Err())}
		}
	}
}

func (m *manager) waitForSocketWithStage(ctx context.Context, stage, socketPath string, watchers executor.Group) error {
	return m.waitForSockets(ctx, stage, []string{socketPath}, watchers)
}

func (m *manager) effectiveSSHReadyTimeout() time.Duration {
	if m.sshReadyTimeout > 0 {
		return m.sshReadyTimeout
	}
	return defaultSSHReadyTimeout
}
