package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

func TestUnsupportedHotplug(t *testing.T) {
	err := UnsupportedHotplug()
	var rpcErr *control.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error type: got %T", err)
	}
	if rpcErr.Code != control.ErrUnsupported {
		t.Fatalf("code: got %s want %s", rpcErr.Code, control.ErrUnsupported)
	}
}

func TestHotplugQMPRunsRawCommand(t *testing.T) {
	client := &fakeHotplugQMPClient{}
	qmp := HotplugQMP{Client: client, Timeout: 2 * time.Second}

	if err := qmp.Run(context.Background(), `{"execute":"device_add"}`); err != nil {
		t.Fatalf("run: %v", err)
	}
	if client.runTimeout != 2*time.Second || client.command != `{"execute":"device_add"}` {
		t.Fatalf("run call: timeout=%s command=%q", client.runTimeout, client.command)
	}
}

func TestHotplugQMPDeletesDevice(t *testing.T) {
	client := &fakeHotplugQMPClient{}
	qmp := HotplugQMP{Client: client, Timeout: 2 * time.Second}

	if err := qmp.DeviceDel(context.Background(), "disk-cache"); err != nil {
		t.Fatalf("device del: %v", err)
	}
	if client.deviceDelTimeout != 2*time.Second || client.deviceDelID != "disk-cache" {
		t.Fatalf("device del call: timeout=%s id=%q", client.deviceDelTimeout, client.deviceDelID)
	}
}

func TestHotplugQMPHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := HotplugQMP{Client: &fakeHotplugQMPClient{}, Timeout: time.Second}.Run(ctx, "{}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error: got %v want %v", err, context.Canceled)
	}
}

type fakeHotplugQMPClient struct {
	runTimeout       time.Duration
	command          string
	deviceDelTimeout time.Duration
	deviceDelID      string
}

func (c *fakeHotplugQMPClient) WithRaw(time.Duration, func(*rawQMP.Monitor) error) error {
	return nil
}

func (c *fakeHotplugQMPClient) RunRaw(timeout time.Duration, command string) error {
	c.runTimeout = timeout
	c.command = command
	return nil
}

func (c *fakeHotplugQMPClient) DeviceDelAndWait(timeout time.Duration, id string) error {
	c.deviceDelTimeout = timeout
	c.deviceDelID = id
	return nil
}

func (c *fakeHotplugQMPClient) Stop(time.Duration) error { return nil }

func (c *fakeHotplugQMPClient) Cont(time.Duration) error { return nil }

func (c *fakeHotplugQMPClient) QueryStatus(time.Duration) (string, error) { return "", nil }

func (c *fakeHotplugQMPClient) MigrateToFile(time.Duration, string) error { return nil }

func (c *fakeHotplugQMPClient) MigrateIncoming(time.Duration, string) error { return nil }

func (c *fakeHotplugQMPClient) QueryMigrate(time.Duration) (string, error) { return "", nil }

func (c *fakeHotplugQMPClient) Quit(time.Duration) error { return nil }

func (c *fakeHotplugQMPClient) Disconnect() error { return nil }
