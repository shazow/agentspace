// Package manager runs the host-side sandbox launcher lifecycle.
//
// It takes a validated launch manifest, prepares runtime directories and
// volume images, starts the supporting host processes, waits for QMP and SSH
// readiness, and then hands control to the interactive SSH session. Teardown
// also lives here: optional feature tasks stop first, then the active session
// and helper daemons are shut down, and QEMU is asked to exit through QMP
// before any forced process cleanup is used.
package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

const (
	defaultSSHRetryDelay = 1 * time.Second
	defaultShutdownDelay = 15 * time.Second
)

var sshProbeCommand = []string{"true"}

type manager struct {
	locker            locker
	runner            runner
	socketWaiter      socketWaiter
	qmpDialer         qmpDialer
	logger            *log.Logger
	sshRetryDelay     time.Duration
	shutdownDelay     time.Duration
	qmpRetryDelay     time.Duration
	qmpConnectTimeout time.Duration
	qmpQuitTimeout    time.Duration
	signals           <-chan os.Signal
	selfStop          func() error
	pidSignaler       pidSignaler
}

func newManager() *manager {
	return &manager{
		locker:            &fileLocker{},
		runner:            &execRunner{},
		socketWaiter:      &pollingSocketWaiter{},
		qmpDialer:         &socketMonitorDialer{},
		logger:            log.New(os.Stderr, "virtie: ", 0),
		sshRetryDelay:     defaultSSHRetryDelay,
		shutdownDelay:     defaultShutdownDelay,
		qmpRetryDelay:     defaultQMPRetryDelay,
		qmpConnectTimeout: defaultQMPConnectTimeout,
		qmpQuitTimeout:    defaultQMPQuitTimeout,
	}
}

// Launch runs the supported virtie sandbox session.
func Launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) error {
	return newManager().launch(ctx, manifest, remoteCommand)
}

func (m *manager) launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) (err error) {
	if err := manifest.Validate(); err != nil {
		return err
	}

	launchCtx, cancelLaunch := context.WithCancel(ctx)
	defer cancelLaunch()

	signalCh, stopSignals := m.launchSignalChannel()
	signalDone := make(chan struct{})
	sessionSignals := make(chan os.Signal, 8)
	go func() {
		for {
			select {
			case <-signalDone:
				return
			case sig, ok := <-signalCh:
				if !ok {
					return
				}
				switch sig {
				case os.Interrupt, syscall.SIGTERM:
					cancelLaunch()
				case syscall.SIGTSTP, syscall.SIGCONT:
					select {
					case sessionSignals <- sig:
					default:
					}
				}
			}
		}
	}()
	defer close(signalDone)
	defer stopSignals()

	managedSocketPaths, err := manifest.ResolvedSocketPaths()
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	virtioFSSocketPaths, err := manifest.ResolvedVirtioFSSocketPaths()
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	qmpSocketPath, err := manifest.ResolvedQMPSocketPath()
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	volumes := manifest.ResolvedVolumes()

	lock, err := m.locker.Acquire(manifest.ResolvedLockPath())
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, lock.Release)

	if err := removeSuspendState(manifest); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := writeLaunchPID(manifest, os.Getpid()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, func() error {
		return removeLaunchPID(manifest, os.Getpid())
	})

	cid, cidLock, err := m.allocateCID(manifest)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, cidLock.Release)
	m.logger.Printf("allocated vsock cid %d", cid)

	if err := ensureDirectories(manifest.ResolvedPersistenceDirectories()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(managedSocketPaths); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories([]string{qmpSocketPath}); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(volumeImagePaths(volumes)); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := removeSocketPaths(managedSocketPaths); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := removeSocketPaths([]string{qmpSocketPath}); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureVolumeImages(volumes); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}

	var started []*managedProcess
	var qmpClient qmpClient
	var featureTasks managedTaskGroup
	defer func() {
		featureErr := featureTasks.Stop()
		stopErr := m.stopAll(started)
		var disconnectErr error
		if qmpClient != nil {
			disconnectErr = qmpClient.Disconnect()
		}
		cleanupErr := removeSocketPaths(append([]string{qmpSocketPath}, managedSocketPaths...))
		if err == nil {
			err = errors.Join(featureErr, stopErr, disconnectErr, cleanupErr)
		} else if featureErr != nil || stopErr != nil || disconnectErr != nil || cleanupErr != nil {
			err = errors.Join(err, featureErr, stopErr, disconnectErr, cleanupErr)
		}
	}()

	virtiofsd, err := m.startVirtioFSDaemons(manifest)
	if err != nil {
		return &stageError{Stage: "virtiofs startup", Err: err}
	}
	started = append(started, virtiofsd...)

	m.logger.Printf("waiting for virtiofs sockets")
	if err := m.waitForSockets(launchCtx, virtioFSSocketPaths, started...); err != nil {
		return err
	}

	m.logger.Printf("starting qemu")
	qemuSpec, err := buildQEMUSpec(manifest, cid)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	qemu, err := m.startManagedProcess(qemuSpec)
	if err != nil {
		return &stageError{Stage: "vm startup", Err: err}
	}
	started = append(started, qemu)

	m.logger.Printf("waiting for qmp readiness")
	qmpClient, err = m.waitForQMP(launchCtx, qmpSocketPath, qemu)
	if err != nil {
		return err
	}
	qemu.shutdown = func() error {
		return qmpClient.Quit(m.effectiveQMPQuitTimeout())
	}

	m.logger.Printf("waiting for ssh readiness")
	if err := m.waitForSSH(launchCtx, manifest, cid, started...); err != nil {
		return err
	}

	featureTasks = startOptionalFeatureTasks(launchCtx, optionalFeatureRuntime{
		logger:     m.logger,
		qmpTimeout: m.effectiveQMPCommandTimeout(),
	}, manifest, qmpClient)

	m.logger.Printf("starting ssh session")
	session, err := m.startManagedProcess(buildSSHSpec(manifest, cid, remoteCommand, true))
	if err != nil {
		return &stageError{Stage: "active session", Err: err}
	}
	started = append(started, session)

	return m.waitForSession(launchCtx, session, manifest, qmpSocketPath, qmpClient, sessionSignals, started[:len(started)-1]...)
}

func (m *manager) launchSignalChannel() (<-chan os.Signal, func()) {
	if m.signals != nil {
		return m.signals, func() {}
	}

	ch := make(chan os.Signal, 8)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGTSTP, syscall.SIGCONT)
	return ch, func() {
		signal.Stop(ch)
		close(ch)
	}
}

type managedProcess struct {
	name     string
	proc     process
	done     chan error
	shutdown func() error
}

func (m *manager) startManagedProcess(spec processSpec) (*managedProcess, error) {
	proc, err := m.runner.Start(spec)
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

func (m *manager) startVirtioFSDaemons(manifest *manifest.Manifest) ([]*managedProcess, error) {
	daemons, err := manifest.ResolvedVirtioFSDaemons()
	if err != nil {
		return nil, err
	}
	started := make([]*managedProcess, 0, len(daemons))

	for _, daemon := range daemons {
		name := "virtiofsd"
		if daemon.Tag != "" {
			name = fmt.Sprintf("virtiofsd[%s]", daemon.Tag)
			m.logger.Printf("starting virtiofsd [%s]", daemon.Tag)
		} else {
			m.logger.Printf("starting virtiofsd")
		}

		process, err := m.startManagedProcess(processSpec{
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

func (m *manager) allocateCID(manifest *manifest.Manifest) (int, lock, error) {
	for cid := manifest.VSock.CIDRange.Start; cid <= manifest.VSock.CIDRange.End; cid++ {
		lock, err := m.locker.Acquire(manifest.ResolvedVSockLockPath(cid))
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

func (m *manager) waitForSockets(ctx context.Context, socketPaths []string, watchers ...*managedProcess) error {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.socketWaiter.Wait(waitCtx, socketPaths)
	}()

	ticker := time.NewTicker(defaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return &stageError{Stage: "virtiofs startup", Err: err}
			}
			return nil
		case <-ticker.C:
			if err := firstUnexpectedExit("virtiofs startup", watchers...); err != nil {
				return err
			}
		case <-ctx.Done():
			return &stageError{Stage: "virtiofs startup", Err: ctx.Err()}
		}
	}
}

func (m *manager) waitForQMP(ctx context.Context, socketPath string, watchers ...*managedProcess) (qmpClient, error) {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.socketWaiter.Wait(waitCtx, []string{socketPath})
	}()

	ticker := time.NewTicker(defaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return nil, &stageError{Stage: "vm startup", Err: err}
			}
			return m.connectQMP(ctx, socketPath, watchers...)
		case <-ticker.C:
			if err := firstUnexpectedExit("vm startup", watchers...); err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, &stageError{Stage: "vm startup", Err: ctx.Err()}
		}
	}
}

func (m *manager) connectQMP(ctx context.Context, socketPath string, watchers ...*managedProcess) (qmpClient, error) {
	dialer := m.qmpDialer
	if dialer == nil {
		dialer = &socketMonitorDialer{}
	}
	connectTimeout := m.effectiveQMPConnectTimeout()
	retryDelay := m.qmpRetryDelay
	if retryDelay <= 0 {
		retryDelay = defaultQMPRetryDelay
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, &stageError{Stage: "vm startup", Err: ctx.Err()}
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
			return nil, &stageError{Stage: "vm startup", Err: ctx.Err()}
		}

		timer.Reset(retryDelay)
	}
}

func (m *manager) effectiveQMPConnectTimeout() time.Duration {
	if m.qmpConnectTimeout > 0 {
		return m.qmpConnectTimeout
	}
	return defaultQMPConnectTimeout
}

func (m *manager) effectiveQMPQuitTimeout() time.Duration {
	if m.qmpQuitTimeout > 0 {
		return m.qmpQuitTimeout
	}
	return defaultQMPQuitTimeout
}

func (m *manager) effectiveQMPCommandTimeout() time.Duration {
	return m.effectiveQMPConnectTimeout()
}

func (m *manager) waitForSSH(ctx context.Context, manifest *manifest.Manifest, cid int, watchers ...*managedProcess) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	loggedPermissionHint := false

	for {
		select {
		case <-ctx.Done():
			return &stageError{Stage: "ssh readiness", Err: ctx.Err()}
		case <-timer.C:
		}

		if err := firstUnexpectedExit("ssh readiness", watchers...); err != nil {
			return err
		}

		spec := buildSSHSpec(manifest, cid, sshProbeCommand, false)
		var stderr bytes.Buffer
		spec.Stderr = &stderr

		probe, err := m.startManagedProcess(spec)
		if err != nil {
			return &stageError{Stage: "ssh readiness", Err: err}
		}

		ready, err := m.waitForSSHProbe(ctx, probe, watchers...)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		if !loggedPermissionHint && sshPermissionDenied(stderr.String()) {
			m.logger.Printf("ssh readiness failed with Permission denied (publickey); ensure SSH keys are unlocked in ssh-agent with ssh-add")
			loggedPermissionHint = true
		}

		timer.Reset(m.sshRetryDelay)
	}
}

func (m *manager) waitForSSHProbe(ctx context.Context, probe *managedProcess, watchers ...*managedProcess) (bool, error) {
	ticker := time.NewTicker(defaultSocketPollInterval)
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
			stageErr := &stageError{Stage: "ssh readiness", Err: ctx.Err()}
			if abortErr := m.killProcess(probe); abortErr != nil {
				return false, errors.Join(stageErr, abortErr)
			}
			return false, stageErr
		}
	}
}

func sshPermissionDenied(stderr string) bool {
	return strings.Contains(stderr, "Permission denied (publickey")
}

func (m *manager) waitForSession(ctx context.Context, session *managedProcess, manifest *manifest.Manifest, qmpSocketPath string, qmpClient qmpClient, signalCh <-chan os.Signal, watchers ...*managedProcess) error {
	ticker := time.NewTicker(defaultSocketPollInterval)
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
		case sig, ok := <-signalCh:
			if !ok {
				signalCh = nil
				continue
			}
			if err := m.handleSessionSignal(ctx, sig, manifest, qmpSocketPath, qmpClient, session); err != nil {
				return err
			}
		case <-ctx.Done():
			return &stageError{Stage: "active session", Err: ctx.Err()}
		}
	}
}

func (m *manager) handleSessionSignal(ctx context.Context, sig os.Signal, manifest *manifest.Manifest, qmpSocketPath string, qmpClient qmpClient, session *managedProcess) error {
	switch sig {
	case syscall.SIGTSTP:
		if err := m.suspendConnected(manifest, qmpSocketPath, qmpClient); err != nil {
			return err
		}
		if err := signalProcessIfRunning(session, syscall.SIGTSTP); err != nil {
			return &stageError{Stage: "active session", Err: err}
		}
		if err := m.stopSelf(); err != nil {
			return &stageError{Stage: "active session", Err: err}
		}
		if ctx.Err() != nil {
			return &stageError{Stage: "active session", Err: ctx.Err()}
		}
		if err := m.resumeConnected(manifest, qmpClient); err != nil {
			return err
		}
		if err := signalProcessIfRunning(session, syscall.SIGCONT); err != nil {
			return &stageError{Stage: "active session", Err: err}
		}
	case syscall.SIGCONT:
		if err := m.resumeConnected(manifest, qmpClient); err != nil {
			return err
		}
		if err := signalProcessIfRunning(session, syscall.SIGCONT); err != nil {
			return &stageError{Stage: "active session", Err: err}
		}
	}
	return nil
}

func (m *manager) stopSelf() error {
	if m.selfStop != nil {
		return m.selfStop()
	}
	return syscall.Kill(os.Getpid(), syscall.SIGSTOP)
}

func signalProcessIfRunning(process *managedProcess, sig os.Signal) error {
	if process == nil {
		return nil
	}
	if err := process.proc.Signal(sig); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("signal %s: %w", process.name, err)
	}
	return nil
}

func (m *manager) stopAll(processes []*managedProcess) error {
	var errs []error
	for i := len(processes) - 1; i >= 0; i-- {
		if err := m.stopProcess(processes[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *manager) killProcess(process *managedProcess) error {
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

func (m *manager) stopProcess(process *managedProcess) error {
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
			timer := time.NewTimer(m.shutdownDelay)
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

	timer := time.NewTimer(m.shutdownDelay)
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
			return &stageError{
				Stage: stage,
				Err:   fmt.Errorf("%s exited unexpectedly", process.name),
			}
		}

		return wrapCommandError(stage, process.name, err)
	}

	return nil
}

func buildSSHSpec(manifest *manifest.Manifest, cid int, remoteCommand []string, interactive bool) processSpec {
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
	if !interactive {
		args = append(args, "-o", "LogLevel=ERROR")
	}
	args = append(args, manifest.SSHDestination(cid))
	args = append(args, remoteCommand...)

	return processSpec{
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

func volumeImagePaths(volumes []manifest.Volume) []string {
	paths := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		paths = append(paths, volume.ImagePath)
	}
	return paths
}

func ensureVolumeImages(volumes []manifest.Volume) error {
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

func createVolumeImage(volume manifest.Volume) error {
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
		return &commandError{
			Stage:    stage,
			Command:  command,
			ExitCode: exitErr.ExitCode(),
			Err:      err,
		}
	}

	return &commandError{
		Stage:    stage,
		Command:  command,
		ExitCode: -1,
		Err:      err,
	}
}
