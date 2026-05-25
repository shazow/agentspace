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
	defaultQMPRetryDelay       = 200 * time.Millisecond
	defaultQMPConnectTimeout   = 500 * time.Millisecond
	defaultQMPQuitTimeout      = 500 * time.Millisecond
	defaultQMPResumeTimeout    = 5 * time.Second
	defaultQMPMigrationTimeout = 30 * time.Second
)

type qmpClient interface {
	WithRaw(timeout time.Duration, fn func(*rawQMP.Monitor) error) error
	Stop(timeout time.Duration) error
	Cont(timeout time.Duration) error
	SystemWakeup(timeout time.Duration) error
	QueryStatus(timeout time.Duration) (string, error)
	MigrateToFile(timeout time.Duration, path string) error
	MigrateIncoming(timeout time.Duration, path string) error
	QueryMigrate(timeout time.Duration) (string, error)
	Events(ctx context.Context) (<-chan doQMP.Event, error)
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

func (c *socketMonitorClient) Stop(timeout time.Duration) error {
	err := c.WithRaw(timeout, func(monitor *rawQMP.Monitor) error {
		if err := monitor.Stop(); err != nil {
			return fmt.Errorf("qmp stop: %w", err)
		}
		return nil
	})
	if isTimeoutError(err) {
		return fmt.Errorf("qmp stop timed out after %s", timeout)
	}
	return err
}

func (c *socketMonitorClient) Cont(timeout time.Duration) error {
	err := c.WithRaw(timeout, func(monitor *rawQMP.Monitor) error {
		if err := monitor.Cont(); err != nil {
			return fmt.Errorf("qmp cont: %w", err)
		}
		return nil
	})
	if isTimeoutError(err) {
		return fmt.Errorf("qmp cont timed out after %s", timeout)
	}
	return err
}

func (c *socketMonitorClient) SystemWakeup(timeout time.Duration) error {
	err := c.WithRaw(timeout, func(monitor *rawQMP.Monitor) error {
		if err := monitor.SystemWakeup(); err != nil {
			return fmt.Errorf("qmp system_wakeup: %w", err)
		}
		return nil
	})
	if isTimeoutError(err) {
		return fmt.Errorf("qmp system_wakeup timed out after %s", timeout)
	}
	return err
}

func (c *socketMonitorClient) QueryStatus(timeout time.Duration) (string, error) {
	var status string
	err := c.WithRaw(timeout, func(monitor *rawQMP.Monitor) error {
		info, err := monitor.QueryStatus()
		if err != nil {
			return fmt.Errorf("qmp query-status: %w", err)
		}
		status = info.Status.String()
		return nil
	})
	if isTimeoutError(err) {
		return "", fmt.Errorf("qmp query-status timed out after %s", timeout)
	}
	return status, err
}

func (c *socketMonitorClient) Events(ctx context.Context) (<-chan doQMP.Event, error) {
	if c == nil || c.monitor == nil {
		return nil, doQMP.ErrEventsNotSupported
	}
	return c.monitor.Events(ctx)
}

func (c *socketMonitorClient) MigrateToFile(timeout time.Duration, path string) error {
	uri := "file:" + path
	err := c.WithRaw(timeout, func(monitor *rawQMP.Monitor) error {
		if err := monitor.Migrate(uri, nil, nil, nil); err != nil {
			return fmt.Errorf("qmp migrate %q: %w", uri, err)
		}
		return nil
	})
	if isTimeoutError(err) {
		return fmt.Errorf("qmp migrate %q timed out after %s", uri, timeout)
	}
	return err
}

func (c *socketMonitorClient) MigrateIncoming(timeout time.Duration, path string) error {
	uri := "file:" + path
	err := c.WithRaw(timeout, func(monitor *rawQMP.Monitor) error {
		if err := monitor.MigrateIncoming(uri); err != nil {
			return fmt.Errorf("qmp migrate-incoming %q: %w", uri, err)
		}
		return nil
	})
	if isTimeoutError(err) {
		return fmt.Errorf("qmp migrate-incoming %q timed out after %s", uri, timeout)
	}
	return err
}

func (c *socketMonitorClient) QueryMigrate(timeout time.Duration) (string, error) {
	var status string
	err := c.WithRaw(timeout, func(monitor *rawQMP.Monitor) error {
		info, err := monitor.QueryMigrate()
		if err != nil {
			return fmt.Errorf("qmp query-migrate: %w", err)
		}
		if info.Status != nil {
			status = info.Status.String()
		}
		return nil
	})
	if isTimeoutError(err) {
		return "", fmt.Errorf("qmp query-migrate timed out after %s", timeout)
	}
	return status, err
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
	conn      net.Conn
	decoder   *json.Decoder
	timeout   time.Duration
	commandMu sync.Mutex
	events    chan doQMP.Event
	responses chan qmpResponse
	done      chan struct{}
	errMu     sync.Mutex
	err       error
}

type qmpResponse struct {
	message json.RawMessage
	err     error
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

		if _, err := m.readResponse(); err != nil {
			return err
		}
		m.events = make(chan doQMP.Event, 16)
		m.responses = make(chan qmpResponse, 1)
		m.done = make(chan struct{})
		go m.readLoop()
		return nil
	})
}

func (m *deadlineSocketMonitor) Disconnect() error {
	if m == nil || m.conn == nil {
		return nil
	}
	err := m.conn.Close()
	if m.done != nil {
		<-m.done
	}
	return err
}

func (m *deadlineSocketMonitor) Run(command []byte) ([]byte, error) {
	m.commandMu.Lock()
	defer m.commandMu.Unlock()

	var timeout <-chan time.Time
	if m.timeout > 0 {
		if err := m.conn.SetDeadline(time.Now().Add(m.timeout)); err != nil {
			return nil, err
		}
		defer m.conn.SetDeadline(time.Time{})

		timer := time.NewTimer(m.timeout)
		defer timer.Stop()
		timeout = timer.C
	}

	if _, err := m.conn.Write(appendQMPDelimiter(command)); err != nil {
		if isTimeoutError(err) {
			m.closeAfterTimeout()
			return nil, timeoutError{op: "qmp command", timeout: m.timeout}
		}
		return nil, err
	}

	select {
	case response, ok := <-m.responses:
		if !ok {
			if err := m.readError(); err != nil {
				if isTimeoutError(err) {
					return nil, timeoutError{op: "qmp command", timeout: m.timeout}
				}
				return nil, err
			}
			return nil, errors.New("qmp monitor disconnected")
		}
		if response.err != nil {
			return nil, response.err
		}
		return response.message, nil
	case <-timeout:
		m.closeAfterTimeout()
		return nil, timeoutError{op: "qmp command", timeout: m.timeout}
	case <-m.done:
		if err := m.readError(); err != nil {
			if isTimeoutError(err) {
				return nil, timeoutError{op: "qmp command", timeout: m.timeout}
			}
			return nil, err
		}
		return nil, errors.New("qmp monitor disconnected")
	}
}

func (m *deadlineSocketMonitor) closeAfterTimeout() {
	_ = m.conn.Close()
	if m.done != nil {
		<-m.done
	}
}

func (m *deadlineSocketMonitor) Events(ctx context.Context) (<-chan doQMP.Event, error) {
	if m == nil || m.events == nil {
		return nil, doQMP.ErrEventsNotSupported
	}
	if ctx == nil {
		return m.events, nil
	}
	events := make(chan doQMP.Event)
	go func() {
		defer close(events)
		for {
			select {
			case event, ok := <-m.events:
				if !ok {
					return
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
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

func (m *deadlineSocketMonitor) readResponse() ([]byte, error) {
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

func (m *deadlineSocketMonitor) readLoop() {
	defer close(m.done)
	defer close(m.events)
	defer close(m.responses)

	for {
		var message json.RawMessage
		if err := m.decoder.Decode(&message); err != nil {
			m.setReadError(err)
			return
		}

		var envelope struct {
			Event string `json:"event,omitempty"`
			Error *struct {
				Class string `json:"class"`
				Desc  string `json:"desc"`
			} `json:"error,omitempty"`
		}
		if err := json.Unmarshal(message, &envelope); err != nil {
			m.responses <- qmpResponse{err: err}
			continue
		}
		if envelope.Event != "" {
			var event doQMP.Event
			if err := json.Unmarshal(message, &event); err != nil {
				m.responses <- qmpResponse{err: err}
				continue
			}
			m.events <- event
			continue
		}
		if envelope.Error != nil && envelope.Error.Desc != "" {
			m.responses <- qmpResponse{err: errors.New(envelope.Error.Desc)}
			continue
		}
		m.responses <- qmpResponse{message: message}
	}
}

func (m *deadlineSocketMonitor) setReadError(err error) {
	m.errMu.Lock()
	defer m.errMu.Unlock()
	m.err = err
}

func (m *deadlineSocketMonitor) readError() error {
	m.errMu.Lock()
	defer m.errMu.Unlock()
	return m.err
}

type timeoutError struct {
	op      string
	timeout time.Duration
}

func (e timeoutError) Error() string {
	return fmt.Sprintf("%s timed out after %s", e.op, e.timeout)
}

func (e timeoutError) Timeout() bool {
	return true
}

func (e timeoutError) Temporary() bool {
	return true
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
