package manager

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type guestAgentClient interface {
	Ping(timeout time.Duration) error
	OpenFile(timeout time.Duration, path string) (int, error)
	WriteFile(timeout time.Duration, handle int, contentBase64 string) error
	CloseFile(timeout time.Duration, handle int) error
	Disconnect() error
}

type guestAgentDialer interface {
	Dial(ctx context.Context, socketPath string, timeout time.Duration) (guestAgentClient, error)
}

type socketGuestAgentDialer struct{}

type socketGuestAgentClient struct {
	conn    net.Conn
	decoder *json.Decoder
	mu      sync.Mutex
}

func (d *socketGuestAgentDialer) Dial(ctx context.Context, socketPath string, timeout time.Duration) (guestAgentClient, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isTimeoutError(err) {
			return nil, fmt.Errorf("guest agent connect timed out after %s", timeout)
		}
		return nil, err
	}
	if ctx.Err() != nil {
		_ = conn.Close()
		return nil, ctx.Err()
	}

	return &socketGuestAgentClient{
		conn:    conn,
		decoder: json.NewDecoder(conn),
	}, nil
}

func (c *socketGuestAgentClient) Ping(timeout time.Duration) error {
	_, err := c.run(timeout, "guest-ping", nil)
	if err != nil {
		return fmt.Errorf("guest agent ping: %w", err)
	}
	return nil
}

func (c *socketGuestAgentClient) OpenFile(timeout time.Duration, path string) (int, error) {
	response, err := c.run(timeout, "guest-file-open", map[string]any{
		"path": path,
		"mode": "w",
	})
	if err != nil {
		return 0, fmt.Errorf("guest agent open %q: %w", path, err)
	}

	var handle int
	if err := json.Unmarshal(response, &handle); err != nil {
		return 0, fmt.Errorf("guest agent open %q returned invalid handle: %w", path, err)
	}
	return handle, nil
}

func (c *socketGuestAgentClient) WriteFile(timeout time.Duration, handle int, contentBase64 string) error {
	_, err := c.run(timeout, "guest-file-write", map[string]any{
		"handle":  handle,
		"buf-b64": contentBase64,
	})
	if err != nil {
		return fmt.Errorf("guest agent write handle %d: %w", handle, err)
	}
	return nil
}

func (c *socketGuestAgentClient) CloseFile(timeout time.Duration, handle int) error {
	_, err := c.run(timeout, "guest-file-close", map[string]any{
		"handle": handle,
	})
	if err != nil {
		return fmt.Errorf("guest agent close handle %d: %w", handle, err)
	}
	return nil
}

func (c *socketGuestAgentClient) Disconnect() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *socketGuestAgentClient) run(timeout time.Duration, execute string, arguments map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if timeout > 0 {
		if err := c.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return nil, err
		}
		defer c.conn.SetDeadline(time.Time{})
	}

	command := map[string]any{"execute": execute}
	if arguments != nil {
		command["arguments"] = arguments
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(appendQMPDelimiter(payload)); err != nil {
		return nil, err
	}

	for {
		var envelope struct {
			Return json.RawMessage `json:"return"`
			Event  string          `json:"event,omitempty"`
			Error  *struct {
				Class string `json:"class"`
				Desc  string `json:"desc"`
			} `json:"error,omitempty"`
		}
		if err := c.decoder.Decode(&envelope); err != nil {
			return nil, err
		}
		if envelope.Event != "" {
			continue
		}
		if envelope.Error != nil {
			if envelope.Error.Desc != "" {
				return nil, errors.New(envelope.Error.Desc)
			}
			return nil, fmt.Errorf("guest agent command %q failed with %s", execute, envelope.Error.Class)
		}
		return envelope.Return, nil
	}
}

func (m *manager) writeGuestFiles(ctx context.Context, launchManifest *manifest.Manifest, watchers ...*managedProcess) error {
	files := launchManifest.ResolvedWriteFiles()
	if len(files) == 0 {
		return nil
	}

	socketPath, err := launchManifest.ResolvedGuestAgentSocketPath()
	if err != nil {
		return &stageError{Stage: "guest agent", Err: err}
	}

	m.logger.Printf("waiting for guest agent readiness")
	client, err := m.waitForGuestAgent(ctx, socketPath, watchers...)
	if err != nil {
		return err
	}
	defer client.Disconnect()

	for _, file := range files {
		contentBase64, err := guestFileContentBase64(file)
		if err != nil {
			return &stageError{Stage: "guest file write", Err: err}
		}
		if err := m.writeGuestFile(client, file.GuestPath, contentBase64); err != nil {
			return &stageError{Stage: "guest file write", Err: err}
		}
		m.logger.Printf("wrote guest file %s", file.GuestPath)
	}
	return nil
}

func guestFileContentBase64(file manifest.ResolvedWriteFile) (string, error) {
	if file.Content != nil {
		return *file.Content, nil
	}
	if file.HostPath == nil {
		return "", fmt.Errorf("guest file %q has no content or host path", file.GuestPath)
	}

	data, err := os.ReadFile(*file.HostPath)
	if err != nil {
		return "", fmt.Errorf("read host file %q for guest path %q: %w", *file.HostPath, file.GuestPath, err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func (m *manager) writeGuestFile(client guestAgentClient, guestPath string, contentBase64 string) error {
	timeout := m.effectiveQMPCommandTimeout()
	handle, err := client.OpenFile(timeout, guestPath)
	if err != nil {
		return err
	}

	writeErr := client.WriteFile(timeout, handle, contentBase64)
	closeErr := client.CloseFile(timeout, handle)
	if writeErr != nil {
		if closeErr != nil {
			return errors.Join(writeErr, closeErr)
		}
		return writeErr
	}
	return closeErr
}

func (m *manager) waitForGuestAgent(ctx context.Context, socketPath string, watchers ...*managedProcess) (guestAgentClient, error) {
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
				return nil, &stageError{Stage: "guest agent", Err: err}
			}
			return m.connectGuestAgent(ctx, socketPath, watchers...)
		case <-ticker.C:
			if err := firstUnexpectedExit("guest agent", watchers...); err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, &stageError{Stage: "guest agent", Err: ctx.Err()}
		}
	}
}

func (m *manager) connectGuestAgent(ctx context.Context, socketPath string, watchers ...*managedProcess) (guestAgentClient, error) {
	dialer := m.guestAgentDialer
	if dialer == nil {
		dialer = &socketGuestAgentDialer{}
	}
	connectTimeout := m.effectiveQMPConnectTimeout()
	retryDelay := m.qmpRetryDelay
	if retryDelay <= 0 {
		retryDelay = defaultQMPRetryDelay
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, &stageError{Stage: "guest agent", Err: ctx.Err()}
		case <-timer.C:
		}

		if err := firstUnexpectedExit("guest agent", watchers...); err != nil {
			return nil, err
		}

		client, err := dialer.Dial(ctx, socketPath, connectTimeout)
		if err == nil {
			if err := client.Ping(m.effectiveQMPCommandTimeout()); err == nil {
				return client, nil
			}
			_ = client.Disconnect()
		}
		if ctx.Err() != nil {
			return nil, &stageError{Stage: "guest agent", Err: ctx.Err()}
		}

		timer.Reset(retryDelay)
	}
}
