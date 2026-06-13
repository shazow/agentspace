package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type launchHost struct {
	manager *manager
}

func (h launchHost) NewLifecycle(cancel context.CancelFunc) *launch.Lifecycle {
	return launch.NewSignalLifecycle(h.manager.signals, cancel)
}

func (h launchHost) AcquireRuntimeLock(spec launch.RuntimeLockSpec) (*launch.RuntimeLock, error) {
	spec.Locker = h.manager.locker
	return launch.AcquireRuntimeLock(spec)
}

func (h launchHost) AcquireCID(cfg *manifest.Manifest, resumeState *launch.SuspendState) (int, error) {
	return launch.AcquireCID(cfg, resumeState, h.manager.vsockCIDChecker)
}

func (h launchHost) BuildQEMUCommand(cfg *manifest.Manifest, cid int, incoming bool) (*exec.Cmd, error) {
	return buildQEMUCommand(cfg, cid, incoming)
}

func (h launchHost) PrepareRuntimeState(plan *launch.Plan) error {
	if h.manager.logger != nil {
		if plan.ResumeState != nil {
			h.manager.logger.Info("restoring saved vsock cid", "cid", plan.CID)
		} else {
			h.manager.logger.Info("allocated vsock cid", "cid", plan.CID)
		}
	}

	for _, dir := range plan.Manifest.ResolvedPersistenceDirectories() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	for _, path := range plan.RuntimeSocketCleanupFiles() {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	for _, path := range plan.ExternalVirtioFSSocketPaths {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("external virtiofs socket %q does not exist", path)
			}
			return fmt.Errorf("stat external virtiofs socket %q: %w", path, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("external virtiofs socket %q is not a socket", path)
		}
	}
	for _, path := range plan.VolumeImagePaths {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	if err := launch.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles()); err != nil {
		return err
	}
	for _, volume := range plan.Volumes {
		if !volume.AutoCreate {
			continue
		}
		info, err := os.Stat(volume.ImagePath)
		if err == nil {
			if info.IsDir() {
				return fmt.Errorf("volume image %q is a directory", volume.ImagePath)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat volume image %q: %w", volume.ImagePath, err)
		}
		if h.manager.logger != nil {
			h.manager.logger.Info("creating volume image", "path", volume.ImagePath, "size_mib", volume.Size, "fs_type", volume.FSType)
		}
		if err := launch.CreateVolumeImage(volume); err != nil {
			return err
		}
	}
	return nil
}

func (h launchHost) RemoveSocketPaths(paths []string) error {
	return launch.RemoveSocketPaths(paths)
}

func (h launchHost) StartRuns(cid int, cfg *manifest.Manifest) (executor.Group, error) {
	return h.manager.startRuns(cid, cfg)
}

func (h launchHost) StartQEMU(cmd *exec.Cmd) (*executor.Process, error) {
	if h.manager.runner == nil {
		return nil, fmt.Errorf("qemu runner is not configured")
	}
	if h.manager.logger != nil {
		h.manager.logger.Info("starting qemu")
	}
	return h.manager.runner.Start(cmd)
}

func (h launchHost) InstallQMPShutdown(qemu *executor.Process, client qmpclient.Client) {
	if qemu == nil || client == nil {
		return
	}
	qemu.SetShutdown(func() error {
		return client.Quit(h.manager.effectiveQMPQuitTimeout())
	})
}

func (h launchHost) WaitForSockets(ctx context.Context, stage string, socketPaths []string, watchers executor.Group) error {
	return h.manager.waitForSockets(ctx, stage, socketPaths, watchers)
}

func (h launchHost) WaitForQMP(ctx context.Context, socketPath string, watchers executor.Group) (qmpclient.Client, error) {
	return h.manager.waitForQMP(ctx, socketPath, watchers)
}

func (h launchHost) RestoreRuntime(ctx context.Context, plan *launch.Plan, client qmpclient.Client) error {
	return h.manager.restoreLaunchRuntime(ctx, plan, client)
}

func (h launchHost) WriteGuestFiles(ctx context.Context, plan *launch.Plan, stats *launch.Stats, watchers executor.Group) error {
	return h.manager.writeGuestFiles(ctx, plan.Manifest, stats, watchers)
}

func (h launchHost) WriteBackGuestFiles(ctx context.Context, plan *launch.Plan, watchers executor.Group) error {
	return h.manager.writeBackGuestFiles(ctx, plan.Manifest, watchers)
}

func (h launchHost) WaitForSSHReady(ctx context.Context, socketPath string, watchers executor.Group) error {
	return h.manager.waitForSSHReady(ctx, socketPath, watchers)
}

func (h launchHost) ShutdownDelay() time.Duration {
	return h.manager.shutdownDelay
}

func (h launchHost) StatsOutput() io.Writer {
	return h.manager.outputWriter()
}
