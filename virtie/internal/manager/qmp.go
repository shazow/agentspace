package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	doQMP "github.com/digitalocean/go-qemu/qmp"
	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
)

const (
	defaultQMPRetryDelay     = 200 * time.Millisecond
	defaultQMPConnectTimeout = 500 * time.Millisecond
	defaultQMPQuitTimeout    = 500 * time.Millisecond
)

type qmpClient interface {
	WithRaw(timeout time.Duration, fn func(*rawQMP.Monitor) error) error
	Quit(timeout time.Duration) error
	Disconnect() error
}

type qmpDialer interface {
	Dial(ctx context.Context, socketPath string, timeout time.Duration) (qmpClient, error)
}

type socketMonitorDialer struct{}

type socketMonitorClient struct {
	monitor *deadlineSocketMonitor
	raw     *rawQMP.Monitor
	mu      sync.Mutex
}

func (d *socketMonitorDialer) Dial(ctx context.Context, socketPath string, timeout time.Duration) (qmpClient, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	monitor, err := newDeadlineSocketMonitor(ctx, "unix", socketPath, timeout)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isTimeoutError(err) {
			return nil, fmt.Errorf("qmp connect timed out after %s", timeout)
		}
		return nil, err
	}

	monitor.setTimeout(timeout)
	interruptDone := make(chan struct{})
	stopInterrupt := make(chan struct{})
	go func() {
		defer close(interruptDone)
		select {
		case <-ctx.Done():
			_ = monitor.interrupt()
		case <-stopInterrupt:
		}
	}()

	err = monitor.Connect()
	close(stopInterrupt)
	<-interruptDone
	if err != nil {
		_ = monitor.Disconnect()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isTimeoutError(err) {
			return nil, fmt.Errorf("qmp connect timed out after %s", timeout)
		}
		return nil, err
	}
	if ctx.Err() != nil {
		_ = monitor.Disconnect()
		return nil, ctx.Err()
	}

	return &socketMonitorClient{
		monitor: monitor,
		raw:     rawQMP.NewMonitor(monitor),
	}, nil
}

func (c *socketMonitorClient) WithRaw(timeout time.Duration, fn func(*rawQMP.Monitor) error) error {
	return c.withTimeout(timeout, func() error {
		return fn(c.raw)
	})
}

func (c *socketMonitorClient) Quit(timeout time.Duration) error {
	err := c.WithRaw(timeout, func(monitor *rawQMP.Monitor) error {
		if err := monitor.Quit(); err != nil {
			return fmt.Errorf("qmp quit: %w", err)
		}
		return nil
	})
	if isTimeoutError(err) {
		return fmt.Errorf("qmp quit timed out after %s", timeout)
	}
	return err
}

func (c *socketMonitorClient) Disconnect() error {
	if c == nil || c.monitor == nil {
		return nil
	}
	return c.monitor.Disconnect()
}

func (c *socketMonitorClient) withTimeout(timeout time.Duration, fn func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.monitor.setTimeout(timeout)
	defer c.monitor.setTimeout(0)

	return fn()
}

type deadlineSocketMonitor struct {
	conn    net.Conn
	decoder *json.Decoder
	timeout time.Duration
	mu      sync.Mutex
}

func newDeadlineSocketMonitor(ctx context.Context, network string, addr string, timeout time.Duration) (*deadlineSocketMonitor, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}

	return &deadlineSocketMonitor{
		conn:    conn,
		decoder: json.NewDecoder(conn),
		timeout: timeout,
	}, nil
}

func (m *deadlineSocketMonitor) Connect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.withDeadline(func() error {
		var banner struct {
			QMP struct {
				Version      doQMP.Version `json:"version"`
				Capabilities []string      `json:"capabilities"`
			} `json:"QMP"`
		}
		if err := m.decoder.Decode(&banner); err != nil {
			return err
		}

		payload, err := json.Marshal(doQMP.Command{Execute: "qmp_capabilities"})
		if err != nil {
			return err
		}
		if _, err := m.conn.Write(appendQMPDelimiter(payload)); err != nil {
			return err
		}

		_, err = m.readResponseLocked()
		return err
	})
}

func (m *deadlineSocketMonitor) Disconnect() error {
	if m == nil || m.conn == nil {
		return nil
	}
	return m.conn.Close()
}

func (m *deadlineSocketMonitor) Run(command []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var response []byte
	err := m.withDeadline(func() error {
		if _, err := m.conn.Write(appendQMPDelimiter(command)); err != nil {
			return err
		}

		var err error
		response, err = m.readResponseLocked()
		return err
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (m *deadlineSocketMonitor) Events(context.Context) (<-chan doQMP.Event, error) {
	return nil, doQMP.ErrEventsNotSupported
}

func (m *deadlineSocketMonitor) setTimeout(timeout time.Duration) {
	m.timeout = timeout
}

func (m *deadlineSocketMonitor) interrupt() error {
	if m == nil || m.conn == nil {
		return nil
	}
	return m.conn.SetDeadline(time.Now())
}

func (m *deadlineSocketMonitor) withDeadline(fn func() error) error {
	if m.timeout > 0 {
		if err := m.conn.SetDeadline(time.Now().Add(m.timeout)); err != nil {
			return err
		}
		defer m.conn.SetDeadline(time.Time{})
	}
	return fn()
}

func (m *deadlineSocketMonitor) readResponseLocked() ([]byte, error) {
	for {
		var message json.RawMessage
		if err := m.decoder.Decode(&message); err != nil {
			return nil, err
		}

		var envelope struct {
			Event string `json:"event,omitempty"`
			Error *struct {
				Class string `json:"class"`
				Desc  string `json:"desc"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(message, &envelope); err != nil {
			return nil, err
		}
		if envelope.Event != "" {
			continue
		}
		if envelope.Error != nil && envelope.Error.Desc != "" {
			return nil, errors.New(envelope.Error.Desc)
		}
		return message, nil
	}
}

func appendQMPDelimiter(command []byte) []byte {
	if len(command) > 0 && command[len(command)-1] == '\n' {
		return command
	}
	return append(append([]byte(nil), command...), '\n')
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
