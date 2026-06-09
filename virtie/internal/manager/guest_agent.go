package manager

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qga"
)

type guestAgentClient = qga.Client
type guestAgentDialer = qga.Dialer
type socketGuestAgentDialer = qga.SocketDialer
type guestExecStatus = qga.ExecStatus

func (m *manager) writeGuestFiles(ctx context.Context, launchManifest *manifest.Manifest, stats *launchStats, watchers executor.Group) error {
	files := launchManifest.ResolvedWriteFiles()
	mountCWD := launchManifest.Workspace.MountCWD
	if len(files) == 0 && !mountCWD {
		return nil
	}

	socketPath, err := launchManifest.ResolvedGuestAgentSocketPath()
	if err != nil {
		return &stageError{Stage: "guest agent", Err: err}
	}

	m.logger.Info("waiting for guest agent readiness")
	client, err := m.waitForGuestAgent(ctx, socketPath, watchers)
	if err != nil {
		return err
	}
	if stats != nil {
		stats.MarkGuestAgentReady(time.Now())
	}
	defer client.Disconnect()

	if mountCWD {
		if err := m.mountWorkspaceCWD(ctx, client, launchManifest); err != nil {
			return &stageError{Stage: "workspace cwd mount", Err: err}
		}
	}

	for _, file := range files {
		if !file.Overwrite {
			exists, err := m.guestPathExists(ctx, client, file.GuestPath)
			if err != nil {
				return &stageError{Stage: "guest file write", Err: err}
			}
			if exists {
				m.logger.Info("skipped existing guest file because overwrite is false", "path", file.GuestPath)
				continue
			}
		}
		payloadBase64, err := launch.GuestFilePayloadBase64(file)
		if err != nil {
			return &stageError{Stage: "guest file write", Err: err}
		}
		if err := m.installGuestFileDirectory(ctx, client, file.GuestPath, file.Chown, file.Mode); err != nil {
			return &stageError{Stage: "guest file write", Err: err}
		}
		if err := m.writeGuestFile(client, file.GuestPath, payloadBase64); err != nil {
			return &stageError{Stage: "guest file write", Err: err}
		}
		if file.Chown != "" {
			if err := m.chownGuestFile(ctx, client, file.GuestPath, file.Chown); err != nil {
				return &stageError{Stage: "guest file write", Err: err}
			}
		}
		if file.Mode != "" {
			if err := m.chmodGuestFile(ctx, client, file.GuestPath, file.Mode); err != nil {
				return &stageError{Stage: "guest file write", Err: err}
			}
		}
		m.logger.Info("wrote guest file", "path", file.GuestPath)
	}
	return nil
}

func (m *manager) writeBackGuestFiles(ctx context.Context, launchManifest *manifest.Manifest, watchers executor.Group) error {
	files := launchManifest.ResolvedWriteFiles()
	writeBackFiles := make([]manifest.ResolvedWriteFile, 0, len(files))
	for _, file := range files {
		if file.WriteBack {
			writeBackFiles = append(writeBackFiles, file)
		}
	}
	if len(writeBackFiles) == 0 {
		return nil
	}

	socketPath, err := launchManifest.ResolvedGuestAgentSocketPath()
	if err != nil {
		return &stageError{Stage: "guest file write-back", Err: err}
	}

	m.logger.Info("waiting for guest agent readiness for write-back")
	client, err := m.waitForGuestAgent(ctx, socketPath, watchers)
	if err != nil {
		return &stageError{Stage: "guest file write-back", Err: err}
	}
	defer client.Disconnect()

	for _, file := range writeBackFiles {
		data, err := m.readGuestFile(client, file.GuestPath)
		if err != nil {
			return &stageError{Stage: "guest file write-back", Err: err}
		}
		if file.Content.Kind != manifest.WriteFileContentPath {
			return &stageError{Stage: "guest file write-back", Err: fmt.Errorf("guest file %q has no host path", file.GuestPath)}
		}
		hostPath, err := launch.WriteBackHostPath(file)
		if err != nil {
			return &stageError{Stage: "guest file write-back", Err: err}
		}
		if err := launch.WriteHostFileAtomic(hostPath, data); err != nil {
			return &stageError{Stage: "guest file write-back", Err: fmt.Errorf("write host file %q from guest path %q: %w", hostPath, file.GuestPath, err)}
		}
		m.logger.Info("wrote guest file back to host", "guest_path", file.GuestPath, "host_path", hostPath)
	}
	return nil
}

func (m *manager) writeGuestFile(client guestAgentClient, guestPath string, payloadBase64 string) error {
	return qga.WriteFile(client, m.effectiveQMPCommandTimeout(), guestPath, payloadBase64)
}

func (m *manager) readGuestFile(client guestAgentClient, guestPath string) ([]byte, error) {
	return qga.ReadFile(client, m.effectiveQMPCommandTimeout(), guestPath, qga.DefaultFileReadChunkSize)
}

const (
	guestChmodPath   = "/run/current-system/sw/bin/chmod"
	guestChownPath   = "/run/current-system/sw/bin/chown"
	guestInstallPath = "/run/current-system/sw/bin/install"
	guestMountPath   = "/run/current-system/sw/bin/mount"
	guestPSPath      = "/run/current-system/sw/bin/ps"
	guestTestPath    = "/run/current-system/sw/bin/test"
)

func (m *manager) mountWorkspaceCWD(ctx context.Context, client guestAgentClient, launchManifest *manifest.Manifest) error {
	baseDir := launchManifest.Workspace.GuestDir
	if baseDir == "" {
		return fmt.Errorf("workspace.guest_dir is required when workspace.mount_cwd is true")
	}
	name := filepath.Base(launchManifest.Paths.WorkingDir)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return fmt.Errorf("derive workspace cwd name from working directory %q", launchManifest.Paths.WorkingDir)
	}
	target := path.Join(baseDir, name)
	if err := m.runGuestFileCommand(ctx, client, "install -d", guestInstallPath, []string{"-d", baseDir, target}, target); err != nil {
		return err
	}
	if err := m.runGuestFileCommand(ctx, client, "mount --bind", guestMountPath, []string{"--bind", "/mnt/cwd", target}, target); err != nil {
		return err
	}
	m.logger.Info("mounted workspace cwd", "source", "/mnt/cwd", "target", target)
	return nil
}

// installGuestFileDirectory ensures that the parent directory for guestPath exists.
// It walks upward until it finds an existing ancestor, then creates only the
// missing directories from top to bottom. owner and mode are passed to install(1)
// for newly-created directories only; existing directories are left unchanged.
// mode is expected to be a file mode and is converted to a directory mode by
// adding execute bits wherever read bits are set.
func (m *manager) installGuestFileDirectory(ctx context.Context, client guestAgentClient, guestPath string, owner string, mode string) error {
	return launch.InstallGuestFileDirectory(ctx, launch.GuestDirectoryInstaller{
		Exists: func(ctx context.Context, guestDir string) (bool, error) {
			return m.guestDirectoryExists(ctx, client, guestDir)
		},
		Install: func(ctx context.Context, guestDir string, args []string) error {
			return m.runGuestFileCommand(ctx, client, "install -d", guestInstallPath, args, guestDir)
		},
	}, guestPath, owner, mode)
}

func (m *manager) guestDirectoryExists(ctx context.Context, client guestAgentClient, guestDir string) (bool, error) {
	status, err := m.runGuestFileCommandStatus(ctx, client, "test -d", guestTestPath, []string{"-d", guestDir}, guestDir)
	if err != nil {
		return false, err
	}
	return status.ExitCode == 0, nil
}

func (m *manager) guestPathExists(ctx context.Context, client guestAgentClient, guestPath string) (bool, error) {
	status, err := m.runGuestFileCommandStatus(ctx, client, "test -e", guestTestPath, []string{"-e", guestPath}, guestPath)
	if err != nil {
		return false, err
	}
	return status.ExitCode == 0, nil
}

func (m *manager) chownGuestFile(ctx context.Context, client guestAgentClient, guestPath string, owner string) error {
	return m.runGuestFileCommand(ctx, client, "chown", guestChownPath, []string{owner, guestPath}, guestPath)
}

func (m *manager) chmodGuestFile(ctx context.Context, client guestAgentClient, guestPath string, mode string) error {
	return m.runGuestFileCommand(ctx, client, "chmod", guestChmodPath, []string{mode, guestPath}, guestPath)
}

func (m *manager) runGuestFileCommand(ctx context.Context, client guestAgentClient, name string, path string, args []string, guestPath string) error {
	status, err := m.runGuestFileCommandStatus(ctx, client, name, path, args, guestPath)
	if err != nil {
		return err
	}
	if status.ExitCode != 0 {
		return fmt.Errorf("%s %q exited with status %d%s", name, guestPath, status.ExitCode, qga.ExecOutputSuffix(status))
	}
	return nil
}

func (m *manager) runGuestFileCommandStatus(ctx context.Context, client guestAgentClient, name string, path string, args []string, guestPath string) (guestExecStatus, error) {
	return m.runGuestCommandStatus(ctx, client, name, path, args, guestPath)
}

func (m *manager) runGuestCommandStatus(ctx context.Context, client guestAgentClient, name string, path string, args []string, subject string) (guestExecStatus, error) {
	return qga.RunCommandStatus(ctx, client, qga.ExecWait{
		Timeout:       m.effectiveQMPCommandTimeout(),
		PollDelay:     defaultMigrationPollDelay,
		Name:          name,
		Path:          path,
		Args:          args,
		Subject:       subject,
		CaptureOutput: true,
	})
}

func (m *manager) waitForGuestAgent(ctx context.Context, socketPath string, watchers executor.Group) (guestAgentClient, error) {
	if err := m.waitForLaunchSockets(ctx, "guest agent", []string{socketPath}, watchers); err != nil {
		return nil, err
	}
	return m.connectGuestAgent(ctx, socketPath, watchers)
}

func (m *manager) connectGuestAgent(ctx context.Context, socketPath string, watchers executor.Group) (guestAgentClient, error) {
	dialer := m.guestAgentDialer
	if dialer == nil {
		dialer = &socketGuestAgentDialer{}
	}
	retryDelay := m.qmpRetryDelay
	if retryDelay <= 0 {
		retryDelay = defaultQMPRetryDelay
	}
	client, err := qga.DialWithRetry(ctx, dialer, qga.DialRetry{
		SocketPath:     socketPath,
		ConnectTimeout: m.effectiveQMPConnectTimeout(),
		CommandTimeout: m.effectiveQMPCommandTimeout(),
		RetryDelay:     retryDelay,
		Check: func() error {
			return firstUnexpectedExit("guest agent", watchers)
		},
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, &stageError{Stage: "guest agent", Err: ctx.Err()}
		}
		return nil, err
	}
	return client, nil
}
