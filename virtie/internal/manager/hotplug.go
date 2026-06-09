//go:build !virtie_no_hotplug

package manager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/hotplug"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qga"
)

type HotplugOptions struct {
	Detach bool
}

const hotplugBuiltIn = true

func Hotplug(ctx context.Context, manifest *manifest.Manifest, id string, options HotplugOptions) error {
	return newManager().hotplug(ctx, manifest, id, options)
}

func (m *manager) hotplug(ctx context.Context, launchManifest *manifest.Manifest, id string, options HotplugOptions) error {
	if err := launchManifest.Validate(); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	controlSocketPath, err := launchManifest.ResolvedControlSocketPath()
	if err == nil && controlSocketPath != "" {
		_, err := Dial(controlSocketPath).Hotplug(ctx, HotplugRequest{ID: id, Detach: options.Detach})
		if err == nil {
			return nil
		}
		if !isControlSocketUnavailable(err) && !isControlUnsupported(err) {
			return &stageError{Stage: "control hotplug", Err: err}
		}
	}
	runtime, err := m.hotplugRuntime(ctx, launchManifest)
	if err != nil {
		return &stageError{Stage: "hotplug", Err: err}
	}
	defer runtime.QMP.(managerHotplugQMP).client.Disconnect()
	if options.Detach {
		if err := runtime.Detach(ctx, id); err != nil {
			return wrapHotplugError(err)
		}
		return nil
	}
	if err := runtime.Attach(ctx, id); err != nil {
		return wrapHotplugError(err)
	}
	return nil
}

func (m *manager) hotplugRuntime(ctx context.Context, launchManifest *manifest.Manifest) (hotplug.Runtime, error) {
	socketPath, err := launchManifest.ResolvedQMPSocketPath()
	if err != nil {
		return hotplug.Runtime{}, err
	}
	client, err := m.waitForQMP(ctx, socketPath, executor.Group{})
	if err != nil {
		return hotplug.Runtime{}, err
	}
	return hotplug.Runtime{
		StateDir: launchManifest.ResolvedPersistenceStateDir(),
		WorkDir:  launchManifest.Paths.WorkingDir,
		Devices:  launchManifest.Hotplug,
		Start:    managerHotplugStarter{m: m},
		Sockets:  managerHotplugSocketWaiter{m: m},
		QMP:      managerHotplugQMP{client: client, timeout: m.effectiveQMPCommandTimeout()},
		Guest:    managerHotplugGuest{m: m, manifest: launchManifest},
	}, nil
}

func wrapHotplugError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "guest command"):
		return &stageError{Stage: "hotplug guest", Err: err}
	case strings.Contains(message, "qmp"), strings.Contains(message, "device_del"), strings.Contains(message, "chardev"), strings.Contains(message, "netdev"), strings.Contains(message, "blockdev"):
		return &stageError{Stage: "hotplug qmp", Err: err}
	case strings.Contains(message, "state"):
		return &stageError{Stage: "hotplug state", Err: err}
	default:
		return &stageError{Stage: "hotplug", Err: err}
	}
}

type managerHotplugStarter struct {
	m *manager
}

func (s managerHotplugStarter) Start(ctx context.Context, cmd *exec.Cmd) (*executor.Process, error) {
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	proc, err := s.m.startManagedProcess(cmd)
	if err != nil {
		return nil, err
	}
	return proc, nil
}

func (s managerHotplugStarter) Stop(process *executor.Process) error {
	if process == nil {
		return nil
	}
	return process.Stop(s.m.shutdownDelay)
}

func (s managerHotplugStarter) SignalPIDGroup(pid int, signal syscall.Signal) error {
	return executor.SignalProcessGroup(pid, signal)
}

type managerHotplugSocketWaiter struct {
	m *manager
}

func (w managerHotplugSocketWaiter) Wait(ctx context.Context, stage string, socketPaths []string, process *executor.Process) error {
	if process != nil {
		return w.m.waitForSockets(ctx, stage, socketPaths, executor.NewGroup(process))
	}
	return w.m.waitForSockets(ctx, stage, socketPaths, executor.Group{})
}

type managerHotplugQMP struct {
	client  qmpClient
	timeout time.Duration
}

func (q managerHotplugQMP) Run(ctx context.Context, command string) error {
	return q.client.RunRaw(q.timeout, command)
}

func (q managerHotplugQMP) DeviceDel(ctx context.Context, id string) error {
	return q.client.DeviceDelAndWait(q.timeout, id)
}

type managerHotplugGuest struct {
	m        *manager
	manifest *manifest.Manifest
}

func (g managerHotplugGuest) Run(ctx context.Context, command []string) error {
	if len(command) == 0 {
		return nil
	}
	if command[0] == "" {
		return fmt.Errorf("guest command path is required")
	}
	socketPath, err := g.manifest.ResolvedGuestAgentSocketPath()
	if err != nil {
		return err
	}
	client, err := g.m.waitForGuestAgent(ctx, socketPath, executor.Group{})
	if err != nil {
		return err
	}
	defer client.Disconnect()
	status, err := g.m.runGuestCommandStatus(ctx, client, filepath.Base(command[0]), command[0], command[1:], strings.Join(command, " "))
	if err != nil {
		return err
	}
	if status.ExitCode != 0 {
		return fmt.Errorf("guest command %q exited with status %d%s", strings.Join(command, " "), status.ExitCode, qga.ExecOutputSuffix(status))
	}
	return nil
}

func hotplugStatePath(launchManifest *manifest.Manifest, id string) (string, error) {
	return hotplug.StatePath(launchManifest.ResolvedPersistenceStateDir(), id)
}

func writeHotplugState(path string, state hotplug.State) error {
	return hotplug.WriteState(path, state)
}

func readHotplugState(path string) (hotplug.State, error) {
	return hotplug.ReadState(path)
}
