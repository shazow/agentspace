package manager

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

const (
	defaultSSHReadyTimeout = 2 * time.Minute
	sshReadyTimeoutEnv     = "VIRTIE_SSH_READY_TIMEOUT"
	sshReadyToken          = "READY"
)

type unixSSHReadyDialer struct{}

func configuredSSHReadyTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(sshReadyTimeoutEnv))
	if raw == "" {
		return defaultSSHReadyTimeout
	}

	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		return defaultSSHReadyTimeout
	}
	return timeout
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

func (m *manager) waitForSSHReady(ctx context.Context, socketPath string, watchers ...*managedProcess) error {
	timeout := m.effectiveSSHReadyTimeout()
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := m.waitForSocketWithStage(readyCtx, "vm startup", socketPath, watchers...); err != nil {
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
		errCh <- readSSHReadyToken(reader)
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
			if err := firstUnexpectedExit("vm startup", watchers...); err != nil {
				return err
			}
		case <-readyCtx.Done():
			return &stageError{Stage: "vm startup", Err: fmt.Errorf("wait for ssh readiness: %w", readyCtx.Err())}
		}
	}
}

func (m *manager) waitForSocketWithStage(ctx context.Context, stage, socketPath string, watchers ...*managedProcess) error {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.socketWaiter.Wait(waitCtx, []string{socketPath})
	}()

	ticker := time.NewTicker(defaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return &stageError{Stage: stage, Err: err}
			}
			return nil
		case <-ticker.C:
			if err := firstUnexpectedExit(stage, watchers...); err != nil {
				return err
			}
		case <-ctx.Done():
			return &stageError{Stage: stage, Err: ctx.Err()}
		}
	}
}

func readSSHReadyToken(reader io.Reader) error {
	var data bytes.Buffer
	buf := make([]byte, 32)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			data.Write(buf[:n])
			token := strings.TrimSpace(data.String())
			if token == sshReadyToken {
				return nil
			}
			if token != "" && !strings.HasPrefix(sshReadyToken, token) {
				return fmt.Errorf("unexpected ssh readiness token %q", truncateReadyToken(token))
			}
		}
		if err == nil {
			continue
		}
		if err != io.EOF {
			return fmt.Errorf("read ssh readiness token: %w", err)
		}
		token := strings.TrimSpace(data.String())
		return fmt.Errorf("unexpected ssh readiness token %q", truncateReadyToken(token))
	}
}

func truncateReadyToken(token string) string {
	const limit = 64
	if len(token) <= limit {
		return token
	}
	var buf bytes.Buffer
	buf.WriteString(token[:limit])
	buf.WriteString("...")
	return buf.String()
}

func (m *manager) effectiveSSHReadyTimeout() time.Duration {
	if m.sshReadyTimeout > 0 {
		return m.sshReadyTimeout
	}
	return defaultSSHReadyTimeout
}
