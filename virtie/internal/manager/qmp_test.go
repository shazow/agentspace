package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
)

func TestQMPClientQuit(t *testing.T) {
	client, commands, cleanup := newTestQMPClient(t, func(message map[string]any) map[string]any {
		return map[string]any{"return": map[string]any{}}
	})
	defer cleanup()

	if err := client.Quit(time.Second); err != nil {
		t.Fatalf("quit: %v", err)
	}

	assertHandshakeCommand(t, commands)
	assertQMPCommand(t, commands, "quit")
}

func TestQMPClientWithRawRunsGenericQMPCommand(t *testing.T) {
	client, commands, cleanup := newTestQMPClient(t, func(message map[string]any) map[string]any {
		return map[string]any{
			"return": map[string]any{
				"running":    true,
				"singlestep": false,
				"status":     "running",
			},
		}
	})
	defer cleanup()

	err := client.WithRaw(time.Second, func(monitor *rawQMP.Monitor) error {
		_, err := monitor.QueryStatus()
		return err
	})
	if err != nil {
		t.Fatalf("with raw query-status: %v", err)
	}

	assertHandshakeCommand(t, commands)
	assertQMPCommand(t, commands, "query-status")
}

func TestQMPClientStopContAndQueryStatus(t *testing.T) {
	status := "running"
	client, commands, cleanup := newTestQMPClient(t, func(message map[string]any) map[string]any {
		switch message["execute"] {
		case "query-status":
			return map[string]any{
				"return": map[string]any{
					"running":    status == "running",
					"singlestep": false,
					"status":     status,
				},
			}
		case "stop":
			status = "paused"
			return map[string]any{"return": map[string]any{}}
		case "cont":
			status = "running"
			return map[string]any{"return": map[string]any{}}
		default:
			return map[string]any{"return": map[string]any{}}
		}
	})
	defer cleanup()

	gotStatus, err := client.QueryStatus(time.Second)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	if gotStatus != "running" {
		t.Fatalf("unexpected status: got %q want running", gotStatus)
	}
	if err := client.Stop(time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	gotStatus, err = client.QueryStatus(time.Second)
	if err != nil {
		t.Fatalf("query status after stop: %v", err)
	}
	if gotStatus != "paused" {
		t.Fatalf("unexpected status after stop: got %q want paused", gotStatus)
	}
	if err := client.Cont(time.Second); err != nil {
		t.Fatalf("cont: %v", err)
	}

	assertHandshakeCommand(t, commands)
	assertQMPCommand(t, commands, "query-status")
	assertQMPCommand(t, commands, "stop")
	assertQMPCommand(t, commands, "query-status")
	assertQMPCommand(t, commands, "cont")
}

func TestQMPClientDeliversEventsWhileWaitingForCommandResponse(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()

		encoder := json.NewEncoder(serverConn)
		decoder := json.NewDecoder(serverConn)
		if err := encoder.Encode(map[string]any{
			"QMP": map[string]any{
				"version": map[string]any{
					"qemu":    map[string]any{"major": 8, "minor": 2, "micro": 0},
					"package": "",
				},
				"capabilities": []string{},
			},
		}); err != nil {
			return
		}

		var handshake map[string]any
		if err := decoder.Decode(&handshake); err != nil {
			return
		}
		if err := encoder.Encode(map[string]any{"return": map[string]any{}}); err != nil {
			return
		}

		var command map[string]any
		if err := decoder.Decode(&command); err != nil {
			return
		}
		if err := encoder.Encode(map[string]any{"event": "SUSPEND"}); err != nil {
			return
		}
		_ = encoder.Encode(map[string]any{
			"return": map[string]any{
				"running":    false,
				"singlestep": false,
				"status":     "suspended",
			},
		})
	}()

	monitor := &deadlineSocketMonitor{
		conn:    clientConn,
		decoder: json.NewDecoder(clientConn),
		timeout: time.Second,
	}
	if err := monitor.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	client := &socketMonitorClient{
		monitor: monitor,
		raw:     rawQMP.NewMonitor(monitor),
	}
	defer client.Disconnect()

	events, err := client.Events(context.Background())
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	status, err := client.QueryStatus(time.Second)
	if err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "suspended" {
		t.Fatalf("unexpected status: got %q want suspended", status)
	}

	select {
	case event := <-events:
		if event.Event != "SUSPEND" {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for qmp event")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for qmp test server")
	}
}

func TestQMPClientClosesMonitorAfterCommandTimeout(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	firstCommandRead := make(chan struct{})
	allowLateResponse := make(chan struct{})
	secondCommandRead := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()

		encoder := json.NewEncoder(serverConn)
		decoder := json.NewDecoder(serverConn)
		if err := encoder.Encode(map[string]any{
			"QMP": map[string]any{
				"version": map[string]any{
					"qemu":    map[string]any{"major": 8, "minor": 2, "micro": 0},
					"package": "",
				},
				"capabilities": []string{},
			},
		}); err != nil {
			return
		}

		var handshake map[string]any
		if err := decoder.Decode(&handshake); err != nil {
			return
		}
		if err := encoder.Encode(map[string]any{"return": map[string]any{}}); err != nil {
			return
		}

		var command map[string]any
		if err := decoder.Decode(&command); err != nil {
			return
		}
		close(firstCommandRead)
		<-allowLateResponse
		if err := encoder.Encode(map[string]any{
			"return": map[string]any{
				"running":    true,
				"singlestep": false,
				"status":     "running",
			},
		}); err != nil {
			return
		}

		if err := decoder.Decode(&command); err == nil {
			secondCommandRead <- struct{}{}
		}
	}()

	monitor := &deadlineSocketMonitor{
		conn:    clientConn,
		decoder: json.NewDecoder(clientConn),
		timeout: time.Second,
	}
	if err := monitor.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	client := &socketMonitorClient{
		monitor: monitor,
		raw:     rawQMP.NewMonitor(monitor),
	}
	defer client.Disconnect()

	_, err := client.QueryStatus(10 * time.Millisecond)
	if err == nil {
		t.Fatal("expected qmp command timeout")
	}
	select {
	case <-firstCommandRead:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first qmp command")
	}

	close(allowLateResponse)
	_, err = client.QueryStatus(time.Second)
	if err == nil {
		t.Fatal("expected closed qmp monitor after command timeout")
	}

	select {
	case <-secondCommandRead:
		t.Fatal("expected qmp monitor to reject commands after timeout")
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for qmp test server to exit")
	}
}

func TestDeadlineSocketMonitorCommandTimeoutCoversWrite(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	done := make(chan struct{})
	close(done)
	monitor := &deadlineSocketMonitor{
		conn:      clientConn,
		responses: make(chan qmpResponse),
		done:      done,
		timeout:   10 * time.Millisecond,
	}

	_, err := monitor.Run(bytes.Repeat([]byte("x"), 1024*1024))
	if err == nil {
		t.Fatal("expected qmp command write timeout")
	}
	if !isTimeoutError(err) {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestQMPClientMigrationCommands(t *testing.T) {
	client, commands, cleanup := newTestQMPClient(t, func(message map[string]any) map[string]any {
		switch message["execute"] {
		case "query-migrate":
			return map[string]any{"return": map[string]any{"status": "completed"}}
		default:
			return map[string]any{"return": map[string]any{}}
		}
	})
	defer cleanup()

	if err := client.MigrateToFile(time.Second, "/tmp/vm.state"); err != nil {
		t.Fatalf("migrate to file: %v", err)
	}
	status, err := client.QueryMigrate(time.Second)
	if err != nil {
		t.Fatalf("query migrate: %v", err)
	}
	if status != "completed" {
		t.Fatalf("unexpected migration status: got %q want completed", status)
	}
	if err := client.MigrateIncoming(time.Second, "/tmp/vm.state"); err != nil {
		t.Fatalf("migrate incoming: %v", err)
	}

	assertHandshakeCommand(t, commands)
	assertQMPCommand(t, commands, "migrate")
	assertQMPCommand(t, commands, "query-migrate")
	assertQMPCommand(t, commands, "migrate-incoming")
}

func TestQMPDialContextCancelsDuringHandshake(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "qmp.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix socket: %v", err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		accepted <- conn
		var buf [1]byte
		_, _ = conn.Read(buf[:])
	}()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	_, err = (&socketMonitorDialer{}).Dial(ctx, socketPath, 5*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("expected handshake cancellation to return promptly, took %s", elapsed)
	}

	select {
	case conn := <-accepted:
		_ = conn.Close()
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for qmp client to connect")
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for qmp test server to exit")
	}
}

func newTestQMPClient(t *testing.T, handler func(message map[string]any) map[string]any) (*socketMonitorClient, <-chan map[string]any, func()) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	commands := make(chan map[string]any, 8)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(commands)
		defer serverConn.Close()

		encoder := json.NewEncoder(serverConn)
		decoder := json.NewDecoder(serverConn)

		if err := encoder.Encode(map[string]any{
			"QMP": map[string]any{
				"version": map[string]any{
					"qemu": map[string]any{
						"major": 8,
						"minor": 2,
						"micro": 0,
					},
					"package": "",
				},
				"capabilities": []string{},
			},
		}); err != nil {
			return
		}

		var handshake map[string]any
		if err := decoder.Decode(&handshake); err != nil {
			return
		}
		commands <- handshake
		if err := encoder.Encode(map[string]any{"return": map[string]any{}}); err != nil {
			return
		}

		for {
			var message map[string]any
			if err := decoder.Decode(&message); err != nil {
				return
			}
			commands <- message

			response := handler(message)
			if response == nil {
				response = map[string]any{"return": map[string]any{}}
			}
			if err := encoder.Encode(response); err != nil {
				return
			}
		}
	}()

	monitor := &deadlineSocketMonitor{
		conn:    clientConn,
		decoder: json.NewDecoder(clientConn),
		timeout: time.Second,
	}
	if err := monitor.Connect(); err != nil {
		t.Fatalf("connect qmp test monitor: %v", err)
	}

	client := &socketMonitorClient{
		monitor: monitor,
		raw:     rawQMP.NewMonitor(monitor),
	}

	cleanup := func() {
		_ = client.Disconnect()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for qmp test server to exit")
		}
	}

	return client, commands, cleanup
}

func assertHandshakeCommand(t *testing.T, commands <-chan map[string]any) {
	t.Helper()
	assertQMPCommand(t, commands, "qmp_capabilities")
}

func assertQMPCommand(t *testing.T, commands <-chan map[string]any, want string) map[string]any {
	t.Helper()

	select {
	case message := <-commands:
		if got := message["execute"]; got != want {
			t.Fatalf("unexpected qmp command: got %v want %v", got, want)
		}
		return message
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for qmp command %q", want)
	}
	return nil
}

func commandArguments(t *testing.T, message map[string]any) map[string]any {
	t.Helper()

	args, ok := message["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("expected command arguments, got %#v", message["arguments"])
	}
	return args
}
