// Package manager runs the host-side sandbox launcher lifecycle.
//
// It takes a validated launch manifest, prepares runtime directories and
// volume images, starts the supporting host processes, waits for QMP readiness,
// and then either hands control to the interactive SSH session or keeps the VM
// lifecycle in the foreground for out-of-band SSH. Teardown also
// lives here: optional feature tasks stop first, then any active session and
// helper daemons are shut down, and QEMU is asked to exit through QMP before
// any forced process cleanup is used.
package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

const (
	defaultSSHRetryDelay      = 1 * time.Second
	defaultShutdownDelay      = 15 * time.Second
	defaultMigrationPollDelay = 100 * time.Millisecond
	sshFailureOutputLimit     = 64 * 1024
	sshRetryOutputRevealDelay = 250 * time.Millisecond
)

var errSavedSuspendExit = errors.New("saved suspend requested")

type ResumeMode string

const (
	ResumeModeNo    ResumeMode = "no"
	ResumeModeAuto  ResumeMode = "auto"
	ResumeModeForce ResumeMode = "force"
)

type LaunchOptions struct {
	Resume    ResumeMode
	SSH       bool
	Verbosity int
}

type manager struct {
	locker              locker
	runner              runner
	socketWaiter        socketWaiter
	qmpDialer           qmpDialer
	guestAgentDialer    guestAgentDialer
	logger              *slog.Logger
	logWriter           io.Writer
	sshRetryDelay       time.Duration
	shutdownDelay       time.Duration
	qmpRetryDelay       time.Duration
	qmpConnectTimeout   time.Duration
	qmpQuitTimeout      time.Duration
	qmpMigrationTimeout time.Duration
	signals             <-chan os.Signal
	pidSignaler         pidSignaler
	notificationRunner  notificationRunner
	notifier            notificationSink
}

func newManager() *manager {
	logWriter := io.Writer(os.Stderr)
	return &manager{
		locker:              &fileLocker{},
		runner:              &execRunner{},
		socketWaiter:        &pollingSocketWaiter{},
		qmpDialer:           &socketMonitorDialer{},
		guestAgentDialer:    &socketGuestAgentDialer{},
		logger:              logger,
		logWriter:           logWriter,
		sshRetryDelay:       defaultSSHRetryDelay,
		shutdownDelay:       defaultShutdownDelay,
		qmpRetryDelay:       defaultQMPRetryDelay,
		qmpConnectTimeout:   defaultQMPConnectTimeout,
		qmpQuitTimeout:      defaultQMPQuitTimeout,
		qmpMigrationTimeout: defaultQMPMigrationTimeout,
	}
}

// Launch runs the supported virtie sandbox session.
func Launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) error {
	return newManager().launch(ctx, manifest, remoteCommand)
}

// LaunchWithOptions runs the supported virtie sandbox session with explicit launch options.
func LaunchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options LaunchOptions) error {
	return newManager().launchWithOptions(ctx, manifest, remoteCommand, options)
}

func (m *manager) launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) (err error) {
	return m.launchWithOptions(ctx, manifest, remoteCommand, LaunchOptions{Resume: ResumeModeNo, SSH: true})
}

func (m *manager) launchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options LaunchOptions) (err error) {
	stats := newLaunchStats(time.Now())
	if err := manifest.Validate(); err != nil {
		return err
	}
	resumeMode, err := normalizeResumeMode(options.Resume)
	if err != nil {
		return err
	}
	resumeState, err := resolveLaunchResumeState(manifest, resumeMode)
	if err != nil {
		return err
	}
	notifier := m.effectiveNotifier(manifest)

	launchCtx, cancelLaunch := context.WithCancel(ctx)
	defer cancelLaunch()

	signalCh, stopSignals := m.launchSignalChannel()
	suspendRequests := make(chan struct{}, 1)
	infoRequests := make(chan struct{}, 1)
	signalDone := make(chan struct{})
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
				case syscall.SIGTSTP:
					select {
					case suspendRequests <- struct{}{}:
					default:
					}
				case syscall.SIGUSR1:
					select {
					case infoRequests <- struct{}{}:
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
	guestAgentSocketPath, err := manifest.ResolvedGuestAgentSocketPath()
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	volumes := manifest.ResolvedVolumes()

	lock, err := m.locker.Acquire(manifest.ResolvedLockPath())
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, lock.Release)

	if resumeState == nil {
		if err := removeSuspendState(manifest); err != nil {
			return &stageError{Stage: "preflight", Err: err}
		}
	}
	if err := ensureSavedVMStateAvailable(resumeState); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := writeLaunchPID(manifest, os.Getpid()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, func() error {
		return removeLaunchPID(manifest, os.Getpid())
	})

	cid, cidLock, err := m.acquireLaunchCID(manifest, resumeState)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, cidLock.Release)
	if resumeState != nil {
		m.logger.Info("restoring saved vsock cid", "cid", cid)
	} else {
		m.logger.Info("allocated vsock cid", "cid", cid)
	}

	if err := ensureDirectories(manifest.ResolvedPersistenceDirectories()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(managedSocketPaths); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories([]string{qmpSocketPath}); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if guestAgentSocketPath != "" {
		if err := ensureParentDirectories([]string{guestAgentSocketPath}); err != nil {
			return &stageError{Stage: "preflight", Err: err}
		}
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
	if guestAgentSocketPath != "" {
		if err := removeSocketPaths([]string{guestAgentSocketPath}); err != nil {
			return &stageError{Stage: "preflight", Err: err}
		}
	}
	if err := ensureVolumeImages(volumes); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}

	var started []*managedProcess
	var qmpClient qmpClient
	var featureTasks managedTaskGroup
	defer func() {
		if errors.Is(err, errSavedSuspendExit) {
			err = nil
		}
		featureErr := featureTasks.Stop()
		stopErr := m.stopAll(started)
		var disconnectErr error
		if qmpClient != nil {
			disconnectErr = qmpClient.Disconnect()
		}
		cleanupPaths := append([]string{qmpSocketPath}, managedSocketPaths...)
		if guestAgentSocketPath != "" {
			cleanupPaths = append(cleanupPaths, guestAgentSocketPath)
		}
		cleanupErr := removeSocketPaths(cleanupPaths)
		if err == nil {
			err = errors.Join(featureErr, stopErr, disconnectErr, cleanupErr)
		} else if featureErr != nil || stopErr != nil || disconnectErr != nil || cleanupErr != nil {
			err = errors.Join(err, featureErr, stopErr, disconnectErr, cleanupErr)
		}
		stats.MarkCompleted(time.Now())
		fmt.Fprintf(m.outputWriter(), "stats: %s\n", stats.String())
	}()

	virtiofsd, err := m.startVirtioFSDaemons(manifest)
	if err != nil {
		return &stageError{Stage: "virtiofs startup", Err: err}
	}
	started = append(started, virtiofsd...)

	m.logger.Info("waiting for virtiofs sockets")
	if err := m.waitForSockets(launchCtx, virtioFSSocketPaths, started...); err != nil {
		return err
	}

	if resumeState != nil {
		m.logger.Info("starting qemu for restore")
	} else {
		m.logger.Info("starting qemu")
	}
	stats.MarkBootStarted(time.Now())
	qemuSpec, err := buildLaunchQEMUSpec(manifest, cid, resumeState != nil)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	qemu, err := m.startManagedProcess(qemuSpec)
	if err != nil {
		return &stageError{Stage: "vm startup", Err: err}
	}
	started = append(started, qemu)

	m.logger.Info("waiting for qmp readiness")
	qmpClient, err = m.waitForQMP(launchCtx, qmpSocketPath, qemu)
	if err != nil {
		return err
	}
	qemu.shutdown = func() error {
		return qmpClient.Quit(m.effectiveQMPQuitTimeout())
	}
	if resumeState != nil {
		m.logger.Info("restoring vm state", "path", resumeState.VMStatePath)
		if err := qmpClient.MigrateIncoming(m.effectiveQMPMigrationTimeout(), resumeState.VMStatePath); err != nil {
			return &stageError{Stage: "restore", Err: err}
		}
		if err := m.waitForMigration(launchCtx, qmpClient); err != nil {
			return &stageError{Stage: "restore", Err: err}
		}
		if err := qmpClient.Cont(m.effectiveQMPCommandTimeout()); err != nil {
			return &stageError{Stage: "restore", Err: err}
		}
		notifier.Notify(launchCtx, notifyStateRuntimeResume, "Restored saved VM state", map[string]string{
			"host_name":     manifest.Identity.HostName,
			"vm_state_path": resumeState.VMStatePath,
			"cid":           fmt.Sprintf("%d", cid),
		})
	}
	suspendHandler := newLaunchSuspendHandler(m, manifest, qmpSocketPath, qmpClient, cid, notifier)
	if err := m.handlePendingSuspendRequest(launchCtx, suspendRequests, suspendHandler); err != nil {
		return err
	}

	if resumeState == nil {
		if err := m.writeGuestFiles(launchCtx, manifest, qemu); err != nil {
			return err
		}
	}

	featureTasks = startOptionalFeatureTasks(launchCtx, optionalFeatureRuntime{
		qmpTimeout: m.effectiveQMPCommandTimeout(),
		notifier:   notifier,
	}, manifest, qmpClient)

	if options.SSH {
		sshRetryLog := newSSHRetryLogger(m.logger)
		for {
			stats.MarkSSHStarted(time.Now())
			spec := buildSSHSpec(manifest, cid, remoteCommand)
			stderr := newSSHRetryOutput(os.Stderr, options.Verbosity > 0)
			spec.Stderr = stderr

			session, err := m.startManagedProcess(spec)
			if err != nil {
				return &stageError{Stage: "active session", Err: err}
			}
			started = append(started, session)

			if err := m.waitForSession(launchCtx, session, suspendRequests, infoRequests, suspendHandler, guestAgentSocketPath, started[:len(started)-1]...); err != nil {
				output := stderr.String()
				if sshTransientStartupFailure(err, output) {
					stderr.Suppress()
					sshRetryLog.Log(err, output)
					if options.Verbosity > 1 {
						m.logger.Info("ssh retry failed", "err", err)
					}
					started = started[:len(started)-1]
					select {
					case <-time.After(m.sshRetryDelay):
						continue
					case <-suspendRequests:
						return suspendHandler.saveAndExit(launchCtx)
					case <-infoRequests:
						m.printGuestInfo(launchCtx, guestAgentSocketPath, started...)
						continue
					case <-launchCtx.Done():
						return &stageError{Stage: "active session", Err: launchCtx.Err()}
					}
				}
				stderr.Flush()
				return err
			}
			stderr.Flush()
			if resumeState != nil {
				if err := os.Remove(resumeState.VMStatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
					return &stageError{Stage: "restore", Err: fmt.Errorf("remove saved vm state %q: %w", resumeState.VMStatePath, err)}
				}
				if err := removeSuspendState(manifest); err != nil {
					return &stageError{Stage: "restore", Err: err}
				}
			}
			return nil
		}
	}

	if resumeState != nil {
		if err := os.Remove(resumeState.VMStatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return &stageError{Stage: "restore", Err: fmt.Errorf("remove saved vm state %q: %w", resumeState.VMStatePath, err)}
		}
		if err := removeSuspendState(manifest); err != nil {
			return &stageError{Stage: "restore", Err: err}
		}
	}

	fmt.Fprintf(m.outputWriter(), "connect with: %s\n", buildSSHCommandHint(manifest, cid))
	if err := m.waitForVM(launchCtx, qemu, suspendRequests, infoRequests, suspendHandler, guestAgentSocketPath, started[:len(started)-1]...); err != nil {
		return err
	}
	return nil
}

func normalizeResumeMode(mode ResumeMode) (ResumeMode, error) {
	switch mode {
	case "", ResumeModeNo:
		return ResumeModeNo, nil
	case ResumeModeAuto, ResumeModeForce:
		return mode, nil
	default:
		return "", &stageError{Stage: "preflight", Err: fmt.Errorf("unsupported resume mode %q", mode)}
	}
}

func resolveLaunchResumeState(manifest *manifest.Manifest, mode ResumeMode) (*suspendState, error) {
	if mode == ResumeModeNo {
		return nil, nil
	}

	state, err := readSuspendState(manifest)
	if err != nil {
		if os.IsNotExist(err) && mode == ResumeModeAuto {
			return nil, nil
		}
		if os.IsNotExist(err) {
			return nil, &stageError{Stage: "restore", Err: fmt.Errorf("no saved suspend state found at %q; run virtie suspend first", suspendStatePath(manifest))}
		}
		return nil, &stageError{Stage: "restore", Err: err}
	}
	if state.Status != "saved" {
		if mode == ResumeModeAuto {
			return nil, nil
		}
		return nil, &stageError{Stage: "restore", Err: fmt.Errorf("suspend state %q has status %q, not saved; run virtie suspend first", suspendStatePath(manifest), state.Status)}
	}
	if state.CID <= 0 {
		if mode == ResumeModeAuto {
			return nil, nil
		}
		return nil, &stageError{Stage: "restore", Err: fmt.Errorf("saved suspend state %q does not include a valid vsock CID", suspendStatePath(manifest))}
	}
	if state.VMStatePath == "" {
		state.VMStatePath = vmStatePath(manifest)
	}
	if _, err := os.Stat(state.VMStatePath); err != nil {
		if mode == ResumeModeAuto {
			return nil, nil
		}
		return nil, &stageError{Stage: "restore", Err: fmt.Errorf("saved vm state %q is not available: %w", state.VMStatePath, err)}
	}
	return &state, nil
}

func ensureSavedVMStateAvailable(state *suspendState) error {
	if state == nil {
		return nil
	}
	if _, err := os.Stat(state.VMStatePath); err != nil {
		return fmt.Errorf("saved vm state %q is not available: %w", state.VMStatePath, err)
	}
	return nil
}

func (m *manager) acquireLaunchCID(manifest *manifest.Manifest, state *suspendState) (int, lock, error) {
	if state == nil {
		return m.allocateCID(manifest)
	}
	lock, err := m.acquireCID(manifest, state.CID)
	if err != nil {
		return 0, nil, err
	}
	return state.CID, lock, nil
}

func buildLaunchQEMUSpec(manifest *manifest.Manifest, cid int, resume bool) (processSpec, error) {
	if resume {
		return buildIncomingQEMUSpec(manifest, cid)
	}
	return buildQEMUSpec(manifest, cid)
}

func (m *manager) launchSignalChannel() (<-chan os.Signal, func()) {
	if m.signals != nil {
		return m.signals, func() {}
	}

	ch := make(chan os.Signal, 8)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGTSTP, syscall.SIGUSR1)
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
			m.logger.Info("starting virtiofsd", "tag", daemon.Tag)
		} else {
			m.logger.Info("starting virtiofsd")
		}

		process, err := m.startManagedProcess(processSpec{
			Name:         name,
			Path:         daemon.Command.Path,
			Args:         daemon.Command.Args,
			Dir:          manifest.Paths.WorkingDir,
			Env:          []string{fmt.Sprintf("VIRTIE_SOCKET_PATH=%s", daemon.SocketPath)},
			ProcessGroup: true,
			Stdout:       os.Stderr,
			Stderr:       os.Stderr,
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

func (m *manager) acquireCID(manifest *manifest.Manifest, cid int) (lock, error) {
	if cid < manifest.VSock.CIDRange.Start || cid > manifest.VSock.CIDRange.End {
		return nil, fmt.Errorf("saved vsock CID %d is outside manifest range %d-%d", cid, manifest.VSock.CIDRange.Start, manifest.VSock.CIDRange.End)
	}
	lock, err := m.locker.Acquire(manifest.ResolvedVSockLockPath(cid))
	if err != nil {
		return nil, err
	}
	return lock, nil
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

func (m *manager) effectiveQMPMigrationTimeout() time.Duration {
	if m.qmpMigrationTimeout > 0 {
		return m.qmpMigrationTimeout
	}
	return defaultQMPMigrationTimeout
}

func (m *manager) effectiveQMPCommandTimeout() time.Duration {
	return m.effectiveQMPConnectTimeout()
}

type launchSuspendHandler struct {
	manager       *manager
	manifest      *manifest.Manifest
	qmpSocketPath string
	client        qmpClient
	cid           int
	notifier      notificationSink
	once          sync.Once
	err           error
}

func newLaunchSuspendHandler(manager *manager, manifest *manifest.Manifest, qmpSocketPath string, client qmpClient, cid int, notifier notificationSink) *launchSuspendHandler {
	return &launchSuspendHandler{
		manager:       manager,
		manifest:      manifest,
		qmpSocketPath: qmpSocketPath,
		client:        client,
		cid:           cid,
		notifier:      notifier,
	}
}

func (h *launchSuspendHandler) saveAndExit(ctx context.Context) error {
	h.once.Do(func() {
		if err := h.manager.saveSuspendStateConnected(ctx, h.manifest, h.qmpSocketPath, h.client, h.cid, h.notifier); err != nil {
			h.err = err
			return
		}
		h.err = errSavedSuspendExit
	})
	return h.err
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.limit <= b.Len() {
		return n, nil
	}
	remaining := b.limit - b.Len()
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

type sshRetryOutput struct {
	mu          sync.Mutex
	output      io.Writer
	verbose     bool
	captured    cappedBuffer
	pending     cappedBuffer
	timer       *time.Timer
	revealed    bool
	suppressed  bool
	revealDelay time.Duration
}

func newSSHRetryOutput(output io.Writer, verbose bool) *sshRetryOutput {
	return &sshRetryOutput{
		output:      output,
		verbose:     verbose,
		captured:    cappedBuffer{limit: sshFailureOutputLimit},
		pending:     cappedBuffer{limit: sshFailureOutputLimit},
		revealDelay: sshRetryOutputRevealDelay,
	}
}

func (o *sshRetryOutput) Write(p []byte) (int, error) {
	if o == nil {
		return len(p), nil
	}

	o.mu.Lock()
	_, _ = o.captured.Write(p)
	if o.verbose || o.revealed {
		output := o.output
		o.mu.Unlock()
		if output != nil {
			_, _ = output.Write(p)
		}
		return len(p), nil
	}
	if !o.suppressed {
		_, _ = o.pending.Write(p)
		if o.timer == nil {
			o.timer = time.AfterFunc(o.revealDelay, o.Flush)
		}
	}
	o.mu.Unlock()
	return len(p), nil
}

func (o *sshRetryOutput) String() string {
	if o == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.captured.String()
}

func (o *sshRetryOutput) Suppress() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	o.pending.Reset()
	o.suppressed = true
}

func (o *sshRetryOutput) Flush() {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.suppressed && !o.verbose {
		o.mu.Unlock()
		return
	}
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
	o.revealed = true
	output := o.output
	pending := append([]byte(nil), o.pending.Bytes()...)
	o.pending.Reset()
	o.mu.Unlock()

	if output != nil && len(pending) > 0 {
		_, _ = output.Write(pending)
	}
}

func sshTransientStartupFailure(err error, stderr string) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error() + "\n" + stderr)
	transientMessages := []string{
		"connection refused",
		"connection timed out",
		"no route to host",
		"network is unreachable",
		"connection reset",
		"connection closed",
	}
	for _, transient := range transientMessages {
		if strings.Contains(message, transient) {
			return true
		}
	}
	return false
}

type sshRetryPhase int

const (
	sshRetryPhaseNone sshRetryPhase = iota
	sshRetryPhaseWaiting
	sshRetryPhaseConnecting
)

type sshRetryLogger struct {
	logger *slog.Logger
	seen   map[sshRetryPhase]bool
}

func newSSHRetryLogger(logger *slog.Logger) *sshRetryLogger {
	return &sshRetryLogger{
		logger: logger,
		seen:   make(map[sshRetryPhase]bool),
	}
}

func (l *sshRetryLogger) Log(err error, stderr string) {
	phase := sshRetryPhaseForFailure(err, stderr)
	if phase == sshRetryPhaseNone || l.seen[phase] {
		return
	}
	l.seen[phase] = true
	switch phase {
	case sshRetryPhaseWaiting:
		l.logger.Info("waiting for ssh connection")
	case sshRetryPhaseConnecting:
		l.logger.Info("connecting ssh")
	}
}

func sshRetryPhaseForFailure(err error, stderr string) sshRetryPhase {
	message := strings.ToLower("")
	if err != nil {
		message = strings.ToLower(err.Error() + "\n" + stderr)
	} else {
		message = strings.ToLower(stderr)
	}
	connectingMessages := []string{
		"connection reset",
		"connection closed",
	}
	for _, transient := range connectingMessages {
		if strings.Contains(message, transient) {
			return sshRetryPhaseConnecting
		}
	}
	waitingMessages := []string{
		"connection refused",
		"connection timed out",
		"no route to host",
		"network is unreachable",
	}
	for _, transient := range waitingMessages {
		if strings.Contains(message, transient) {
			return sshRetryPhaseWaiting
		}
	}
	return sshRetryPhaseNone
}

func (m *manager) waitForSession(ctx context.Context, session *managedProcess, suspendRequests <-chan struct{}, infoRequests <-chan struct{}, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers ...*managedProcess) error {
	ticker := time.NewTicker(defaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-session.done:
			if err != nil {
				return wrapCommandError("active session", session.name, err)
			}
			return nil
		case <-suspendRequests:
			return suspendHandler.saveAndExit(ctx)
		case <-infoRequests:
			m.printGuestInfo(ctx, guestAgentSocketPath, watchers...)
		case <-ticker.C:
			if err := firstUnexpectedExit("active session", watchers...); err != nil {
				return err
			}
		case <-ctx.Done():
			return &stageError{Stage: "active session", Err: ctx.Err()}
		}
	}
}

func (m *manager) waitForVM(ctx context.Context, qemu *managedProcess, suspendRequests <-chan struct{}, infoRequests <-chan struct{}, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers ...*managedProcess) error {
	ticker := time.NewTicker(defaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-qemu.done:
			if err != nil {
				return wrapCommandError("vm session", qemu.name, err)
			}
			return nil
		case <-suspendRequests:
			return suspendHandler.saveAndExit(ctx)
		case <-infoRequests:
			m.printGuestInfo(ctx, guestAgentSocketPath, watchers...)
		case <-ticker.C:
			if err := firstUnexpectedExit("vm session", watchers...); err != nil {
				return err
			}
		case <-ctx.Done():
			return &stageError{Stage: "vm session", Err: ctx.Err()}
		}
	}
}

func (m *manager) handlePendingSuspendRequest(ctx context.Context, suspendRequests <-chan struct{}, suspendHandler *launchSuspendHandler) error {
	select {
	case <-suspendRequests:
		return suspendHandler.saveAndExit(ctx)
	default:
		return nil
	}
}

func (m *manager) saveSuspendStateConnected(ctx context.Context, manifest *manifest.Manifest, qmpSocketPath string, client qmpClient, cid int, notifier notificationSink) error {
	timeout := m.effectiveQMPCommandTimeout()

	status, err := client.QueryStatus(timeout)
	if err != nil {
		return &stageError{Stage: "qmp suspend", Err: err}
	}
	switch status {
	case "paused":
	case "running":
		if err := client.Stop(timeout); err != nil {
			return &stageError{Stage: "qmp suspend", Err: err}
		}
	default:
		return &stageError{Stage: "qmp suspend", Err: fmt.Errorf("cannot save VM while QMP status is %q", status)}
	}

	statePath := vmStatePath(manifest)
	if err := ensureParentDirectories([]string{statePath}); err != nil {
		return &stageError{Stage: "qmp suspend", Err: err}
	}
	if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &stageError{Stage: "qmp suspend", Err: fmt.Errorf("remove stale vm state %q: %w", statePath, err)}
	}
	if err := client.MigrateToFile(m.effectiveQMPMigrationTimeout(), statePath); err != nil {
		return &stageError{Stage: "qmp suspend", Err: err}
	}
	if err := m.waitForMigration(ctx, client); err != nil {
		return &stageError{Stage: "qmp suspend", Err: err}
	}

	if err := writeSuspendStateData(manifest, suspendState{
		HostName:      manifest.Identity.HostName,
		QMPSocketPath: qmpSocketPath,
		VMStatePath:   statePath,
		CID:           cid,
		Status:        "saved",
	}); err != nil {
		return &stageError{Stage: "qmp suspend", Err: err}
	}
	if notifier == nil {
		notifier = noopNotifier{}
	}
	notifier.Notify(ctx, notifyStateRuntimeSuspend, "Saved VM suspend state", map[string]string{
		"host_name":       manifest.Identity.HostName,
		"qmp_socket_path": qmpSocketPath,
		"vm_state_path":   statePath,
		"cid":             fmt.Sprintf("%d", cid),
	})
	return nil
}

func (m *manager) waitForMigration(ctx context.Context, client qmpClient) error {
	timeout := m.effectiveQMPMigrationTimeout()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(defaultMigrationPollDelay)
	defer ticker.Stop()

	var lastStatus string
	for {
		status, err := client.QueryMigrate(m.effectiveQMPCommandTimeout())
		if err != nil {
			return err
		}
		if status != "" {
			lastStatus = status
		}
		switch status {
		case "completed":
			return nil
		case "failed", "cancelled":
			return fmt.Errorf("migration %s", status)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastStatus == "" {
				lastStatus = "unknown"
			}
			return fmt.Errorf("migration did not complete within %s; last status %q", timeout, lastStatus)
		case <-ticker.C:
		}
	}
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

func buildSSHSpec(manifest *manifest.Manifest, cid int, remoteCommand []string) processSpec {
	argv := append([]string(nil), manifest.SSH.Argv...)
	path := argv[0]
	args := append([]string(nil), argv[1:]...)

	args = append([]string{"-tt"}, args...)
	args = append(args, manifest.SSHDestination(cid))
	if len(remoteCommand) > 0 {
		args = append(args, encodeRemoteCommand(remoteCommand))
	}

	return processSpec{
		Name:   "ssh",
		Path:   path,
		Args:   args,
		Dir:    manifest.Paths.WorkingDir,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

func buildSSHCommandHint(manifest *manifest.Manifest, cid int) string {
	args := append([]string(nil), manifest.SSH.Argv...)
	args = append(args, manifest.SSHDestination(cid))
	return shellQuoteArgs(args)
}

func encodeRemoteCommand(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return shellQuoteArgs(args)
}

func shellQuoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if shellSafeArg(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func shellSafeArg(arg string) bool {
	for _, ch := range arg {
		switch {
		case ch >= 'A' && ch <= 'Z':
		case ch >= 'a' && ch <= 'z':
		case ch >= '0' && ch <= '9':
		case strings.ContainsRune("_@%+=:,./-", ch):
		default:
			return false
		}
	}
	return true
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
