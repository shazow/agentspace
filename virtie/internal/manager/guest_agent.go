package manager

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
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
		payloadBase64, err := guestFilePayloadBase64(file)
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

const guestFileReadChunkSize = 1024 * 1024

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
		hostPath, err := writeBackHostPath(file)
		if err != nil {
			return &stageError{Stage: "guest file write-back", Err: err}
		}
		if err := writeHostFileAtomic(hostPath, data); err != nil {
			return &stageError{Stage: "guest file write-back", Err: fmt.Errorf("write host file %q from guest path %q: %w", hostPath, file.GuestPath, err)}
		}
		m.logger.Info("wrote guest file back to host", "guest_path", file.GuestPath, "host_path", hostPath)
	}
	return nil
}

func guestFilePayloadBase64(file manifest.ResolvedWriteFile) (string, error) {
	if file.Content.Kind == manifest.WriteFileContentText {
		return base64.StdEncoding.EncodeToString([]byte(file.Content.Text)), nil
	}
	if file.Content.Kind != manifest.WriteFileContentPath {
		return "", fmt.Errorf("guest file %q has no text or host path", file.GuestPath)
	}

	data, err := readHostFileForGuest(file)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func readHostFileForGuest(file manifest.ResolvedWriteFile) ([]byte, error) {
	if file.Content.Kind != manifest.WriteFileContentPath {
		return nil, fmt.Errorf("guest file %q has no host path", file.GuestPath)
	}
	if !file.FollowLinks {
		info, err := os.Lstat(file.Content.Path)
		if err != nil {
			return nil, fmt.Errorf("stat host file %q for guest path %q: %w", file.Content.Path, file.GuestPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("host file %q for guest path %q is a symlink and followLinks is false", file.Content.Path, file.GuestPath)
		}
	}
	data, err := os.ReadFile(file.Content.Path)
	if err != nil {
		return nil, fmt.Errorf("read host file %q for guest path %q: %w", file.Content.Path, file.GuestPath, err)
	}
	return data, nil
}

func writeBackHostPath(file manifest.ResolvedWriteFile) (string, error) {
	if file.Content.Kind != manifest.WriteFileContentPath {
		return "", fmt.Errorf("guest file %q has no host path", file.GuestPath)
	}
	info, err := os.Lstat(file.Content.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return file.Content.Path, nil
		}
		return "", fmt.Errorf("stat host file %q for guest path %q: %w", file.Content.Path, file.GuestPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return file.Content.Path, nil
	}
	if !file.FollowLinks {
		return "", fmt.Errorf("host file %q for guest path %q is a symlink and followLinks is false", file.Content.Path, file.GuestPath)
	}
	resolvedPath, err := filepath.EvalSymlinks(file.Content.Path)
	if err != nil {
		return "", fmt.Errorf("resolve host symlink %q for guest path %q: %w", file.Content.Path, file.GuestPath, err)
	}
	return resolvedPath, nil
}

func (m *manager) writeGuestFile(client guestAgentClient, guestPath string, payloadBase64 string) error {
	timeout := m.effectiveQMPCommandTimeout()
	handle, err := client.OpenFile(timeout, guestPath)
	if err != nil {
		return err
	}

	writeErr := client.WriteFile(timeout, handle, payloadBase64)
	closeErr := client.CloseFile(timeout, handle)
	if writeErr != nil {
		if closeErr != nil {
			return errors.Join(writeErr, closeErr)
		}
		return writeErr
	}
	return closeErr
}

func (m *manager) readGuestFile(client guestAgentClient, guestPath string) ([]byte, error) {
	timeout := m.effectiveQMPCommandTimeout()
	handle, err := client.OpenFileRead(timeout, guestPath)
	if err != nil {
		return nil, err
	}

	var result []byte
	for {
		payloadBase64, eof, readErr := client.ReadFile(timeout, handle, guestFileReadChunkSize)
		if readErr == nil && payloadBase64 != "" {
			chunk, decodeErr := base64.StdEncoding.DecodeString(payloadBase64)
			if decodeErr != nil {
				readErr = fmt.Errorf("decode guest file %q chunk: %w", guestPath, decodeErr)
			} else {
				result = append(result, chunk...)
			}
		}
		if readErr != nil {
			closeErr := client.CloseFile(timeout, handle)
			if closeErr != nil {
				return nil, errors.Join(readErr, closeErr)
			}
			return nil, readErr
		}
		if eof {
			break
		}
	}

	closeErr := client.CloseFile(timeout, handle)
	if closeErr != nil {
		return nil, closeErr
	}
	return result, nil
}

func writeHostFileAtomic(hostPath string, data []byte) error {
	dir := filepath.Dir(hostPath)
	mode := os.FileMode(0o644)
	if info, err := os.Stat(hostPath); err == nil {
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(dir, ".virtie-writeback-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, hostPath); err != nil {
		return err
	}
	cleanup = false
	return nil
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
	guestDir := path.Clean(path.Dir(guestPath))
	if guestDir == "." || guestDir == "/" {
		return nil
	}

	missingDirs := make([]string, 0, 4)
	current := guestDir
	for {
		exists, err := m.guestDirectoryExists(ctx, client, current)
		if err != nil {
			return err
		}
		if exists {
			break
		}
		missingDirs = append(missingDirs, current)
		parent := path.Dir(current)
		if parent == current {
			return fmt.Errorf("resolve existing parent for %q", guestDir)
		}
		current = parent
	}

	for i := len(missingDirs) - 1; i >= 0; i-- {
		dir := missingDirs[i]
		args := guestInstallDirectoryArgs(dir, owner, mode)
		if err := m.runGuestFileCommand(ctx, client, "install -d", guestInstallPath, args, dir); err != nil {
			return err
		}
	}
	return nil
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

func guestInstallDirectoryArgs(guestDir string, owner string, mode string) []string {
	args := []string{"-d"}
	if owner != "" {
		user, group, _ := strings.Cut(owner, ":")
		if user != "" {
			args = append(args, "-o", user)
		}
		if group != "" {
			args = append(args, "-g", group)
		}
	}
	if mode != "" {
		args = append(args, "-m", guestDirectoryMode(mode))
	}
	return append(args, guestDir)
}

func guestDirectoryMode(mode string) string {
	prefix := ""
	digits := mode
	if strings.HasPrefix(mode, "0") {
		prefix = "0"
		digits = mode[1:]
	}
	if len(digits) != 3 {
		return mode
	}

	out := make([]byte, 3)
	for i := 0; i < 3; i++ {
		d := digits[i] - '0'
		if d&0b100 != 0 {
			d |= 0b001
		}
		out[i] = '0' + d
	}
	return prefix + string(out)
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
		return fmt.Errorf("%s %q exited with status %d%s", name, guestPath, status.ExitCode, guestExecOutputSuffix(status))
	}
	return nil
}

func (m *manager) runGuestFileCommandStatus(ctx context.Context, client guestAgentClient, name string, path string, args []string, guestPath string) (guestExecStatus, error) {
	return m.runGuestCommandStatus(ctx, client, name, path, args, guestPath)
}

func (m *manager) runGuestCommandStatus(ctx context.Context, client guestAgentClient, name string, path string, args []string, subject string) (guestExecStatus, error) {
	timeout := m.effectiveQMPCommandTimeout()
	pid, err := client.Exec(timeout, path, args, true)
	if err != nil {
		return guestExecStatus{}, fmt.Errorf("%s %q: %w", name, subject, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return guestExecStatus{}, fmt.Errorf("%s %q timed out after %s", name, subject, timeout)
		}

		status, err := client.ExecStatus(minDuration(timeout, remaining), pid)
		if err != nil {
			return guestExecStatus{}, fmt.Errorf("%s %q: %w", name, subject, err)
		}
		if status.Exited {
			return status, nil
		}

		sleep := minDuration(defaultMigrationPollDelay, time.Until(deadline))
		if sleep <= 0 {
			continue
		}
		timer := time.NewTimer(sleep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return guestExecStatus{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func guestExecOutputSuffix(status guestExecStatus) string {
	stdout := decodeGuestExecData(status.OutData)
	stderr := decodeGuestExecData(status.ErrData)
	switch {
	case stdout != "" && stderr != "":
		return fmt.Sprintf(": stdout=%q stderr=%q", stdout, stderr)
	case stdout != "":
		return fmt.Sprintf(": stdout=%q", stdout)
	case stderr != "":
		return fmt.Sprintf(": stderr=%q", stderr)
	default:
		return ""
	}
}

func decodeGuestExecData(data string) string {
	if data == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return data
	}
	return string(decoded)
}

func minDuration(a time.Duration, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
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
