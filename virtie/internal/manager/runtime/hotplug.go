package runtime

import (
	"context"
	"os/exec"
	"syscall"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type HotplugStarter interface {
	Start(context.Context, *exec.Cmd) (*executor.Process, error)
	Stop(*executor.Process) error
	SignalPIDGroup(int, syscall.Signal) error
}

type HotplugSocketWaiter interface {
	Wait(context.Context, string, []string, *executor.Process) error
}

type HotplugGuest interface {
	Run(context.Context, []string) error
}

type HotplugQMP struct {
	Client  qmpclient.Client
	Timeout time.Duration
}

func (q HotplugQMP) Run(ctx context.Context, command string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return q.Client.RunRaw(q.Timeout, command)
}

func (q HotplugQMP) DeviceDel(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return q.Client.DeviceDelAndWait(q.Timeout, id)
}

func UnsupportedHotplug() error {
	return &control.RPCError{Code: control.ErrUnsupported, Message: "hotplug support is not built into this virtie binary"}
}
