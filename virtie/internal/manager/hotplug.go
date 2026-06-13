package manager

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/shazow/agentspace/virtie/internal/executor"
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qga"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

func Hotplug(ctx context.Context, manifest *manifest.Manifest, id string, detach bool) error {
	return newManager().hotplug(ctx, manifest, id, detach)
}

func (m *manager) hotplug(ctx context.Context, launchManifest *manifest.Manifest, id string, detach bool) error {
	if err := launchManifest.Validate(); err != nil {
		return &launch.StageError{Stage: "preflight", Err: err}
	}
	controlSocketPath, err := launchManifest.ResolvedControlSocketPath()
	if err == nil && controlSocketPath != "" {
		_, err := controlpkg.Dial(controlSocketPath).Hotplug(ctx, controlpkg.HotplugRequest{ID: id, Detach: detach})
		if err == nil {
			return nil
		}
		if !controlpkg.IsSocketUnavailable(err) && !controlpkg.IsUnsupported(err) {
			return &launch.StageError{Stage: "control hotplug", Err: err}
		}
	}
	feature, client, err := m.directHotplugFeature(ctx, launchManifest)
	if err != nil {
		return &launch.StageError{Stage: "hotplug", Err: err}
	}
	defer client.Disconnect()
	if detach {
		_, err := feature.Hotplug(ctx, controlpkg.HotplugRequest{ID: id, Detach: true})
		return err
	}
	_, err = feature.Hotplug(ctx, controlpkg.HotplugRequest{ID: id})
	return err
}

func (m *manager) directHotplugFeature(ctx context.Context, launchManifest *manifest.Manifest) (managerHotplugFeature, qmpclient.Client, error) {
	socketPath, err := launchManifest.ResolvedQMPSocketPath()
	if err != nil {
		return managerHotplugFeature{}, nil, err
	}
	client, err := m.waitForQMP(ctx, socketPath, executor.Group{})
	if err != nil {
		return managerHotplugFeature{}, nil, err
	}
	return m.hotplugFeature(launchManifest, client), client, nil
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
