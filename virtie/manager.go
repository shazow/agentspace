package virtie

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	manifestpkg "github.com/shazow/agentspace/virtie/manifest"
)

const (
	DefaultSSHRetryDelay = 1 * time.Second
	DefaultShutdownDelay = 15 * time.Second
)

var SSHProbeCommand = []string{"true"}

type Manager struct {
	Locker            Locker
	Runner            Runner
	SocketWaiter      SocketWaiter
	QMPDialer         QMPDialer
	Logger            *log.Logger
	SSHRetryDelay     time.Duration
	ShutdownDelay     time.Duration
	QMPRetryDelay     time.Duration
	QMPConnectTimeout time.Duration
	QMPQuitTimeout    time.Duration
}

func NewManager() *Manager {
	return &Manager{
		Locker:            &FileLocker{},
		Runner:            &ExecRunner{},
		SocketWaiter:      &PollingSocketWaiter{},
		QMPDialer:         &socketMonitorDialer{},
		Logger:            log.New(os.Stderr, "virtie: ", 0),
		SSHRetryDelay:     DefaultSSHRetryDelay,
		ShutdownDelay:     DefaultShutdownDelay,
		QMPRetryDelay:     DefaultQMPRetryDelay,
		QMPConnectTimeout: DefaultQMPConnectTimeout,
		QMPQuitTimeout:    DefaultQMPQuitTimeout,
	}
}

func (m *Manager) Launch(ctx context.Context, manifest *manifestpkg.Manifest, remoteCommand []string) (err error) {
	if err := manifest.Validate(); err != nil {
		return err
	}

	socketPaths, err := manifest.ResolvedSocketPaths()
	if err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	qmpSocketPath, err := manifest.ResolvedQMPSocketPath()
	if err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	volumes := manifest.ResolvedVolumes()

	lock, err := m.Locker.Acquire(manifest.ResolvedLockPath())
	if err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, lock.Release)

	cid, cidLock, err := m.allocateCID(manifest)
	if err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, cidLock.Release)
	m.Logger.Printf("allocated vsock cid %d", cid)

	if err := ensureDirectories(manifest.ResolvedPersistenceDirectories()); err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(socketPaths); err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories([]string{qmpSocketPath}); err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(volumeImagePaths(volumes)); err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	if err := removeSocketPaths(socketPaths); err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	if err := removeSocketPaths([]string{qmpSocketPath}); err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	if err := ensureVolumeImages(volumes); err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}

	var started []*managedProcess
	var qmpClient QMPClient
	var featureTask *managedTask
	defer func() {
		featureErr := featureTask.Stop()
		stopErr := m.stopAll(started)
		var disconnectErr error
		if qmpClient != nil {
			disconnectErr = qmpClient.Disconnect()
		}
		cleanupErr := removeSocketPaths(append([]string{qmpSocketPath}, socketPaths...))
		if err == nil {
			err = errors.Join(featureErr, stopErr, disconnectErr, cleanupErr)
		} else if featureErr != nil || stopErr != nil || disconnectErr != nil || cleanupErr != nil {
			err = errors.Join(err, featureErr, stopErr, disconnectErr, cleanupErr)
		}
	}()

	virtiofsd, err := m.startVirtioFSDaemons(manifest)
	if err != nil {
		return &StageError{Stage: "virtiofs startup", Err: err}
	}
	started = append(started, virtiofsd...)

	m.Logger.Printf("waiting for virtiofs sockets")
	if err := m.waitForSockets(ctx, socketPaths, started...); err != nil {
		return err
	}

	m.Logger.Printf("starting qemu")
	qemuSpec, err := buildQEMUSpec(manifest, cid)
	if err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	qemu, err := m.startManagedProcess(qemuSpec)
	if err != nil {
		return &StageError{Stage: "vm startup", Err: err}
	}
	started = append(started, qemu)

	m.Logger.Printf("waiting for qmp readiness")
	qmpClient, err = m.waitForQMP(ctx, qmpSocketPath, qemu)
	if err != nil {
		return err
	}
	qemu.shutdown = func() error {
		return qmpClient.Quit(m.effectiveQMPQuitTimeout())
	}

	m.Logger.Printf("waiting for ssh readiness")
	if err := m.waitForSSH(ctx, manifest, cid, started...); err != nil {
		return err
	}

	featureTask = m.startBalloonController(ctx, manifest, qmpClient)

	m.Logger.Printf("starting ssh session")
	session, err := m.startManagedProcess(buildSSHSpec(manifest, cid, remoteCommand, true))
	if err != nil {
		return &StageError{Stage: "active session", Err: err}
	}
	started = append(started, session)

	return m.waitForSession(ctx, session, started[:len(started)-1]...)
}

type managedProcess struct {
	name     string
	proc     Process
	done     chan error
	shutdown func() error
}

func (m *Manager) startManagedProcess(spec ProcessSpec) (*managedProcess, error) {
	proc, err := m.Runner.Start(spec)
	if err != nil {
		return nil, err
	}

	mp := &managedProcess{
		name: spec.Name,
		proc: proc,
		done: make(chan error, 1),
	}

	go func() {
		mp.done <- proc.Wait()
		close(mp.done)
	}()

	return mp, nil
}

func (m *Manager) startVirtioFSDaemons(manifest *manifestpkg.Manifest) ([]*managedProcess, error) {
	daemons, err := manifest.ResolvedVirtioFSDaemons()
	if err != nil {
		return nil, err
	}
	started := make([]*managedProcess, 0, len(daemons))

	for _, daemon := range daemons {
		name := "virtiofsd"
		if daemon.Tag != "" {
			name = fmt.Sprintf("virtiofsd[%s]", daemon.Tag)
			m.Logger.Printf("starting virtiofsd [%s]", daemon.Tag)
		} else {
			m.Logger.Printf("starting virtiofsd")
		}

		process, err := m.startManagedProcess(ProcessSpec{
			Name:   name,
			Path:   daemon.Command.Path,
			Args:   daemon.Command.Args,
			Dir:    manifest.Paths.WorkingDir,
			Env:    []string{fmt.Sprintf("VIRTIE_SOCKET_PATH=%s", daemon.SocketPath)},
			Stdout: os.Stderr,
			Stderr: os.Stderr,
		})
		if err != nil {
			_ = m.stopAll(started)
			return nil, err
		}

		started = append(started, process)
	}

	return started, nil
}

func (m *Manager) allocateCID(manifest *manifestpkg.Manifest) (int, Lock, error) {
	for cid := manifest.VSock.CIDRange.Start; cid <= manifest.VSock.CIDRange.End; cid++ {
		lock, err := m.Locker.Acquire(manifest.ResolvedVSockLockPath(cid))
		if err == nil {
			return cid, lock, nil
		}
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			continue
		}
		return 0, nil, err
	}

	return 0, nil, fmt.Errorf(
		"no free vsock CID in range %d-%d",
		manifest.VSock.CIDRange.Start,
		manifest.VSock.CIDRange.End,
	)
}

func (m *Manager) waitForSockets(ctx context.Context, socketPaths []string, watchers ...*managedProcess) error {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.SocketWaiter.Wait(waitCtx, socketPaths)
	}()

	ticker := time.NewTicker(DefaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return &StageError{Stage: "virtiofs startup", Err: err}
			}
			return nil
		case <-ticker.C:
			if err := firstUnexpectedExit("virtiofs startup", watchers...); err != nil {
				return err
			}
		case <-ctx.Done():
			return &StageError{Stage: "virtiofs startup", Err: ctx.Err()}
		}
	}
}

func (m *Manager) waitForQMP(ctx context.Context, socketPath string, watchers ...*managedProcess) (QMPClient, error) {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.SocketWaiter.Wait(waitCtx, []string{socketPath})
	}()

	ticker := time.NewTicker(DefaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return nil, &StageError{Stage: "vm startup", Err: err}
			}
			return m.connectQMP(ctx, socketPath, watchers...)
		case <-ticker.C:
			if err := firstUnexpectedExit("vm startup", watchers...); err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, &StageError{Stage: "vm startup", Err: ctx.Err()}
		}
	}
}

func (m *Manager) connectQMP(ctx context.Context, socketPath string, watchers ...*managedProcess) (QMPClient, error) {
	dialer := m.QMPDialer
	if dialer == nil {
		dialer = &socketMonitorDialer{}
	}
	connectTimeout := m.effectiveQMPConnectTimeout()
	retryDelay := m.QMPRetryDelay
	if retryDelay <= 0 {
		retryDelay = DefaultQMPRetryDelay
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, &StageError{Stage: "vm startup", Err: ctx.Err()}
		case <-timer.C:
		}

		if err := firstUnexpectedExit("vm startup", watchers...); err != nil {
			return nil, err
		}

		client, err := dialer.Dial(ctx, socketPath, connectTimeout)
		if err == nil {
			return client, nil
		}
		if ctx.Err() != nil {
			return nil, &StageError{Stage: "vm startup", Err: ctx.Err()}
		}

		timer.Reset(retryDelay)
	}
}

func (m *Manager) effectiveQMPConnectTimeout() time.Duration {
	if m.QMPConnectTimeout > 0 {
		return m.QMPConnectTimeout
	}
	return DefaultQMPConnectTimeout
}

func (m *Manager) effectiveQMPQuitTimeout() time.Duration {
	if m.QMPQuitTimeout > 0 {
		return m.QMPQuitTimeout
	}
	return DefaultQMPQuitTimeout
}

func (m *Manager) effectiveQMPCommandTimeout() time.Duration {
	return m.effectiveQMPConnectTimeout()
}

func (m *Manager) waitForSSH(ctx context.Context, manifest *manifestpkg.Manifest, cid int, watchers ...*managedProcess) error {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return &StageError{Stage: "ssh readiness", Err: ctx.Err()}
		case <-timer.C:
		}

		if err := firstUnexpectedExit("ssh readiness", watchers...); err != nil {
			return err
		}

		probe, err := m.startManagedProcess(buildSSHSpec(manifest, cid, SSHProbeCommand, false))
		if err != nil {
			return &StageError{Stage: "ssh readiness", Err: err}
		}

		ready, err := m.waitForSSHProbe(ctx, probe, watchers...)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}

		timer.Reset(m.SSHRetryDelay)
	}
}

func (m *Manager) waitForSSHProbe(ctx context.Context, probe *managedProcess, watchers ...*managedProcess) (bool, error) {
	ticker := time.NewTicker(DefaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-probe.done:
			return err == nil, nil
		case <-ticker.C:
			if err := firstUnexpectedExit("ssh readiness", watchers...); err != nil {
				if abortErr := m.killProcess(probe); abortErr != nil {
					return false, errors.Join(err, abortErr)
				}
				return false, err
			}
		case <-ctx.Done():
			stageErr := &StageError{Stage: "ssh readiness", Err: ctx.Err()}
			if abortErr := m.killProcess(probe); abortErr != nil {
				return false, errors.Join(stageErr, abortErr)
			}
			return false, stageErr
		}
	}
}

func (m *Manager) waitForSession(ctx context.Context, session *managedProcess, watchers ...*managedProcess) error {
	ticker := time.NewTicker(DefaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-session.done:
			if err != nil {
				return wrapCommandError("active session", session.name, err)
			}
			return nil
		case <-ticker.C:
			if err := firstUnexpectedExit("active session", watchers...); err != nil {
				return err
			}
		case <-ctx.Done():
			return &StageError{Stage: "active session", Err: ctx.Err()}
		}
	}
}

func (m *Manager) stopAll(processes []*managedProcess) error {
	var errs []error
	for i := len(processes) - 1; i >= 0; i-- {
		if err := m.stopProcess(processes[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) killProcess(process *managedProcess) error {
	if process == nil {
		return nil
	}

	if exited, _ := process.pollExit(); exited {
		return nil
	}

	if err := process.proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill %s: %w", process.name, err)
	}

	<-process.done
	return nil
}

func (m *Manager) stopProcess(process *managedProcess) error {
	if process == nil {
		return nil
	}

	if exited, _ := process.pollExit(); exited {
		return nil
	}

	var shutdownErr error
	if process.shutdown != nil {
		if err := process.shutdown(); err != nil {
			shutdownErr = fmt.Errorf("shutdown %s: %w", process.name, err)
		} else {
			timer := time.NewTimer(m.ShutdownDelay)
			defer timer.Stop()

			select {
			case <-process.done:
				return shutdownErr
			case <-timer.C:
			}
		}
	}

	if err := process.proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		if shutdownErr != nil {
			return errors.Join(shutdownErr, fmt.Errorf("stop %s: %w", process.name, err))
		}
		return fmt.Errorf("stop %s: %w", process.name, err)
	}

	timer := time.NewTimer(m.ShutdownDelay)
	defer timer.Stop()

	select {
	case <-process.done:
		return shutdownErr
	case <-timer.C:
		if err := process.proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			if shutdownErr != nil {
				return errors.Join(shutdownErr, fmt.Errorf("kill %s: %w", process.name, err))
			}
			return fmt.Errorf("kill %s: %w", process.name, err)
		}
		<-process.done
		return shutdownErr
	}
}

func (p *managedProcess) pollExit() (bool, error) {
	select {
	case err, ok := <-p.done:
		if !ok {
			return true, nil
		}
		return true, err
	default:
		return false, nil
	}
}

func firstUnexpectedExit(stage string, processes ...*managedProcess) error {
	for _, process := range processes {
		if process == nil {
			continue
		}

		exited, err := process.pollExit()
		if !exited {
			continue
		}

		if err == nil {
			return &StageError{
				Stage: stage,
				Err:   fmt.Errorf("%s exited unexpectedly", process.name),
			}
		}

		return wrapCommandError(stage, process.name, err)
	}

	return nil
}

func buildSSHSpec(manifest *manifestpkg.Manifest, cid int, remoteCommand []string, interactive bool) ProcessSpec {
	argv := append([]string(nil), manifest.SSH.Argv...)
	path := argv[0]
	args := append([]string(nil), argv[1:]...)
	sshModeArgs := []string{}
	stdin := io.Reader(nil)
	stdout := io.Writer(io.Discard)
	stderr := io.Writer(io.Discard)

	if !interactive {
		sshModeArgs = append(
			sshModeArgs,
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=1",
		)
	} else {
		sshModeArgs = append(sshModeArgs, "-tt")
		stdin = os.Stdin
		stdout = os.Stdout
		stderr = os.Stderr
	}

	args = append(sshModeArgs, args...)
	args = append(args, manifest.SSHDestination(cid))
	args = append(args, remoteCommand...)

	return ProcessSpec{
		Name:   "ssh",
		Path:   path,
		Args:   args,
		Dir:    manifest.Paths.WorkingDir,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}
}

func joinDeferredError(target *error, fn func() error) {
	next := fn()
	if next == nil {
		return
	}
	if *target == nil {
		*target = next
		return
	}
	*target = errors.Join(*target, next)
}

func ensureDirectories(directories []string) error {
	for _, dir := range directories {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}

func volumeImagePaths(volumes []manifestpkg.ManifestVolume) []string {
	paths := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		paths = append(paths, volume.ImagePath)
	}
	return paths
}

func ensureVolumeImages(volumes []manifestpkg.ManifestVolume) error {
	for _, volume := range volumes {
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

		if err := createVolumeImage(volume); err != nil {
			return err
		}
	}

	return nil
}

func createVolumeImage(volume manifestpkg.ManifestVolume) error {
	file, err := os.OpenFile(volume.ImagePath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create volume image %q: %w", volume.ImagePath, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close volume image %q: %w", volume.ImagePath, err)
	}

	if chattrPath, lookErr := exec.LookPath("chattr"); lookErr == nil {
		cmd := exec.Command(chattrPath, "+C", volume.ImagePath)
		_ = cmd.Run()
	}

	sizeBytes := int64(volume.SizeMiB) * 1024 * 1024
	if err := os.Truncate(volume.ImagePath, sizeBytes); err != nil {
		return fmt.Errorf("truncate volume image %q: %w", volume.ImagePath, err)
	}

	mkfsArgs := []string{}
	if volume.Label != nil {
		if labelOption := mkfsLabelOption(volume.FSType); labelOption != "" {
			mkfsArgs = append(mkfsArgs, labelOption, *volume.Label)
		}
	}
	mkfsArgs = append(mkfsArgs, volume.MkfsExtraArgs...)
	mkfsArgs = append(mkfsArgs, volume.ImagePath)

	mkfsPath, err := exec.LookPath(fmt.Sprintf("mkfs.%s", volume.FSType))
	if err != nil {
		return fmt.Errorf("find mkfs tool for %q: %w", volume.FSType, err)
	}

	cmd := exec.Command(mkfsPath, mkfsArgs...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("format volume image %q with %s: %w", volume.ImagePath, filepath.Base(mkfsPath), err)
	}

	return nil
}

func mkfsLabelOption(fsType string) string {
	switch fsType {
	case "ext2", "ext3", "ext4", "xfs", "btrfs":
		return "-L"
	case "vfat":
		return "-n"
	default:
		return ""
	}
}

func ensureParentDirectories(paths []string) error {
	for _, path := range paths {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}

func removeSocketPaths(paths []string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove socket %q: %w", path, err)
		}
	}
	return nil
}

func wrapCommandError(stage string, command string, err error) error {
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &CommandError{
			Stage:    stage,
			Command:  command,
			ExitCode: exitErr.ExitCode(),
			Err:      err,
		}
	}

	return &CommandError{
		Stage:    stage,
		Command:  command,
		ExitCode: -1,
		Err:      err,
	}
}
