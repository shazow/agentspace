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
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	backendfile "github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/ext4"
	shellquote "github.com/kballard/go-shellquote"
	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/sshtools"
)

const (
	defaultSSHRetryDelay      = 500 * time.Millisecond
	defaultShutdownDelay      = 15 * time.Second
	defaultMigrationPollDelay = 100 * time.Millisecond
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
	vsockCIDChecker     vsockCIDChecker
	runner              runner
	socketWaiter        socketWaiter
	qmpDialer           qmpDialer
	guestAgentDialer    guestAgentDialer
	sshReadyDialer      sshReadyDialer
	logger              *slog.Logger
	logWriter           io.Writer
	sshRetryDelay       time.Duration
	sshReadyTimeout     time.Duration
	shutdownDelay       time.Duration
	qmpRetryDelay       time.Duration
	qmpConnectTimeout   time.Duration
	qmpQuitTimeout      time.Duration
	qmpMigrationTimeout time.Duration
	signals             <-chan os.Signal
	pidSignaler         pidSignaler
	notifier            notificationSink
}

func newManager() *manager {
	logWriter := io.Writer(os.Stderr)
	return &manager{
		locker:              &fileLocker{},
		vsockCIDChecker:     newHostVSockCIDChecker(),
		runner:              &executor.Runner{},
		socketWaiter:        &pollingSocketWaiter{},
		qmpDialer:           &socketMonitorDialer{},
		guestAgentDialer:    &socketGuestAgentDialer{},
		sshReadyDialer:      &unixSSHReadyDialer{},
		logger:              logger,
		logWriter:           logWriter,
		sshRetryDelay:       defaultSSHRetryDelay,
		sshReadyTimeout:     configuredSSHReadyTimeout(),
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
	if options.SSH && len(remoteCommand) > 0 && len(manifest.SSH.Argv) == 0 {
		return &stageError{Stage: "preflight", Err: fmt.Errorf("remote command arguments require manifest.ssh.exec")}
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

	virtioFSSocketPaths, err := manifest.ResolvedVirtioFSSocketPaths()
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	externalVirtioFSSocketPaths, err := manifest.ResolvedExternalVirtioFSSocketPaths()
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	cleanupFiles, err := manifest.ResolvedCleanupFiles()
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
	sshReadySocketPath, err := manifest.ResolvedSSHReadySocketPath()
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
	if resumeState != nil {
		// Recheck after acquiring the launch lock so restore does not race a
		// concurrent launch/suspend cleanup.
		if _, err := os.Stat(resumeState.VMStatePath); err != nil {
			return &stageError{Stage: "preflight", Err: fmt.Errorf("saved vm state %q is not available: %w", resumeState.VMStatePath, err)}
		}
	}
	if err := writeLaunchPID(manifest, os.Getpid()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, func() error {
		return removeLaunchPID(manifest, os.Getpid())
	})

	cid, err := m.acquireLaunchCID(manifest, resumeState)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if resumeState != nil {
		m.logger.Info("restoring saved vsock cid", "cid", cid)
	} else {
		m.logger.Info("allocated vsock cid", "cid", cid)
	}

	if err := ensureDirectories(manifest.ResolvedPersistenceDirectories()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(cleanupFiles); err != nil {
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
	if sshReadySocketPath != "" {
		if err := ensureParentDirectories([]string{sshReadySocketPath}); err != nil {
			return &stageError{Stage: "preflight", Err: err}
		}
	}
	if err := ensureExistingSocketPaths(externalVirtioFSSocketPaths); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	volumeImagePaths := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		volumeImagePaths = append(volumeImagePaths, volume.ImagePath)
	}
	if err := ensureParentDirectories(volumeImagePaths); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := removeSocketPaths([]string{qmpSocketPath}); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := removeSocketPaths(cleanupFiles); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if guestAgentSocketPath != "" {
		if err := removeSocketPaths([]string{guestAgentSocketPath}); err != nil {
			return &stageError{Stage: "preflight", Err: err}
		}
	}
	if sshReadySocketPath != "" {
		if err := removeSocketPaths([]string{sshReadySocketPath}); err != nil {
			return &stageError{Stage: "preflight", Err: err}
		}
	}
	if err := ensureVolumeImages(volumes, m.logger); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}

	started := executor.NewGroup()
	var qmpClient qmpClient
	var featureTasks managedTaskGroup
	writeBackOnExit := false
	defer func() {
		savedSuspend := errors.Is(err, errSavedSuspendExit)
		if savedSuspend {
			err = nil
		}
		if writeBackOnExit && !savedSuspend {
			writeBackCtx, cancelWriteBack := context.WithTimeout(context.Background(), m.effectiveQMPCommandTimeout())
			writeBackErr := m.writeBackGuestFiles(writeBackCtx, manifest, executor.Group{})
			cancelWriteBack()
			if writeBackErr != nil {
				err = errors.Join(err, writeBackErr)
			}
		}
		featureErr := featureTasks.Stop()
		stopErr := started.StopAll(m.shutdownDelay)
		var disconnectErr error
		if qmpClient != nil {
			disconnectErr = qmpClient.Disconnect()
		}
		socketCleanupFiles := []string{qmpSocketPath}
		if guestAgentSocketPath != "" {
			socketCleanupFiles = append(socketCleanupFiles, guestAgentSocketPath)
		}
		if sshReadySocketPath != "" {
			socketCleanupFiles = append(socketCleanupFiles, sshReadySocketPath)
		}
		socketCleanupFiles = append(socketCleanupFiles, cleanupFiles...)
		cleanupErr := removeSocketPaths(socketCleanupFiles)
		if err == nil {
			err = errors.Join(featureErr, stopErr, disconnectErr, cleanupErr)
		} else if featureErr != nil || stopErr != nil || disconnectErr != nil || cleanupErr != nil {
			err = errors.Join(err, featureErr, stopErr, disconnectErr, cleanupErr)
		}
		stats.MarkCompleted(time.Now())
		fmt.Fprintf(m.outputWriter(), "stats: %s\n", stats.String())
	}()

	runProcesses, err := m.startRuns(cid, manifest)
	if err != nil {
		return err
	}
	started.Add(runProcesses.Processes()...)

	if len(virtioFSSocketPaths) > 0 {
		m.logger.Info("waiting for virtiofs sockets")
		if err := m.waitForSockets(launchCtx, "virtiofs startup", virtioFSSocketPaths, started); err != nil {
			return err
		}
	}

	if resumeState != nil {
		m.logger.Info("starting qemu for restore")
	} else {
		m.logger.Info("starting qemu")
	}
	stats.MarkBootStarted(time.Now())
	qemuCmd, err := buildQEMUCommand(manifest, cid, resumeState != nil)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	qemu, err := m.startManagedProcess(qemuCmd)
	if err != nil {
		return &stageError{Stage: "vm startup", Err: err}
	}
	started.Add(qemu)

	m.logger.Info("waiting for qmp readiness")
	qmpClient, err = m.waitForQMP(launchCtx, qmpSocketPath, started)
	if err != nil {
		return err
	}
	stats.MarkQMPReady(time.Now())
	qemu.SetShutdown(func() error {
		return qmpClient.Quit(m.effectiveQMPQuitTimeout())
	})
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
		writeBackOnExit = true
		notifier.Notify(launchCtx, notifyStateRuntimeResume, "Restored saved VM state", map[string]string{
			"host_name":     manifest.Identity.HostName,
			"vm_state_path": resumeState.VMStatePath,
			"cid":           fmt.Sprintf("%d", cid),
		})
	}
	suspendHandler := newLaunchSuspendHandler(m, manifest, qmpSocketPath, qmpClient, cid, notifier, func() bool {
		return writeBackOnExit
	})
	// Honor a suspend signal queued during startup before guest-file install or
	// SSH startup proceeds.
	select {
	case <-suspendRequests:
		return suspendHandler.saveAndExit(launchCtx)
	default:
	}

	if resumeState == nil {
		if err := m.writeGuestFiles(launchCtx, manifest, stats, started); err != nil {
			return err
		}
		writeBackOnExit = true
		stats.MarkFilesReady(time.Now())

		if sshReadySocketPath != "" {
			m.logger.Info("waiting for ssh readiness")
			if err := m.waitForSSHReady(launchCtx, sshReadySocketPath, started); err != nil {
				return err
			}
		}
		stats.MarkSSHReady(time.Now())
	}

	if options.SSH && len(manifest.SSH.Argv) > 0 {
		featureTasks = startOptionalFeatureTasks(launchCtx, optionalFeatureRuntime{
			qmpTimeout: m.effectiveQMPCommandTimeout(),
			notifier:   notifier,
		}, manifest, qmpClient)

		if err := m.runSSHSession(launchCtx, manifest, cid, remoteCommand, stats, suspendRequests, infoRequests, suspendHandler, guestAgentSocketPath, &started); err != nil {
			return err
		}
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

	featureTasks = startOptionalFeatureTasks(launchCtx, optionalFeatureRuntime{
		qmpTimeout: m.effectiveQMPCommandTimeout(),
		notifier:   notifier,
	}, manifest, qmpClient)

	if resumeState != nil {
		if err := os.Remove(resumeState.VMStatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return &stageError{Stage: "restore", Err: fmt.Errorf("remove saved vm state %q: %w", resumeState.VMStatePath, err)}
		}
		if err := removeSuspendState(manifest); err != nil {
			return &stageError{Stage: "restore", Err: err}
		}
	}

	hint, err := buildSSHCommandHint(manifest, cid)
	if err != nil {
		m.logger.Info("ssh command hint template failed", "err", err)
	} else if hint != "" {
		fmt.Fprintf(m.outputWriter(), "connect with: %s\n", hint)
	}
	vmWatchers := started.Snapshot()
	vmWatchers.Remove(qemu)
	if err := m.waitForVM(launchCtx, qemu, suspendRequests, infoRequests, suspendHandler, guestAgentSocketPath, vmWatchers); err != nil {
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

func (m *manager) acquireLaunchCID(manifest *manifest.Manifest, state *suspendState) (int, error) {
	if state == nil {
		return m.allocateCID(manifest)
	}
	if state.CID < manifest.VSock.CIDRange.Start || state.CID > manifest.VSock.CIDRange.End {
		return 0, fmt.Errorf("saved vsock CID %d is outside manifest range %d-%d", state.CID, manifest.VSock.CIDRange.Start, manifest.VSock.CIDRange.End)
	}
	return state.CID, nil
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

func (m *manager) startManagedProcess(cmd *exec.Cmd) (*executor.Process, error) {
	return m.runner.Start(cmd)
}

func (m *manager) startRuns(cid int, manifest *manifest.Manifest) (executor.Group, error) {
	runs, err := manifest.ResolvedRuns(cid)
	if err != nil {
		return executor.Group{}, &stageError{Stage: "run startup", Err: err}
	}
	if len(runs) == 0 {
		return executor.NewGroup(), nil
	}

	started := executor.NewGroup()
	for i, run := range runs {
		m.logger.Info("starting run", "index", i)
		cmd := executor.Command(run.Exec[0], run.Exec[1:], run.Env)
		cmd.Dir = run.Dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		process, err := m.startManagedProcess(cmd)
		if err != nil {
			_ = started.StopAll(m.shutdownDelay)
			return executor.Group{}, &stageError{Stage: "run startup", Err: err}
		}
		started.Add(process)
	}

	return started, nil
}

func (m *manager) allocateCID(manifest *manifest.Manifest) (int, error) {
	for cid := manifest.VSock.CIDRange.Start; cid <= manifest.VSock.CIDRange.End; cid++ {
		if m.vsockCIDChecker == nil {
			return cid, nil
		}
		available, err := m.vsockCIDChecker.Available(cid)
		if err != nil {
			return 0, err
		}
		if !available {
			continue
		}
		return cid, nil
	}

	return 0, fmt.Errorf(
		"no free vsock CID in range %d-%d",
		manifest.VSock.CIDRange.Start,
		manifest.VSock.CIDRange.End,
	)
}

func (m *manager) waitForSockets(ctx context.Context, stage string, socketPaths []string, watchers executor.Group) error {
	return m.waitForAsyncStage(ctx, stage, watchers, func(waitCtx context.Context) error {
		return m.socketWaiter.Wait(waitCtx, socketPaths)
	})
}

func (m *manager) waitForQMP(ctx context.Context, socketPath string, watchers executor.Group) (qmpClient, error) {
	if err := m.waitForAsyncStage(ctx, "vm startup", watchers, func(waitCtx context.Context) error {
		return m.socketWaiter.Wait(waitCtx, []string{socketPath})
	}); err != nil {
		return nil, err
	}
	return m.connectQMP(ctx, socketPath, watchers)
}

func (m *manager) waitForAsyncStage(ctx context.Context, stage string, watchers executor.Group, wait func(context.Context) error) error {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- wait(waitCtx)
	}()

	ticker := time.NewTicker(defaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return &stageError{Stage: stage, Err: err}
			}
			return nil
		case <-ticker.C:
			if err := firstUnexpectedExit(stage, watchers); err != nil {
				return err
			}
		case <-ctx.Done():
			return &stageError{Stage: stage, Err: ctx.Err()}
		}
	}
}

func (m *manager) connectQMP(ctx context.Context, socketPath string, watchers executor.Group) (qmpClient, error) {
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

		if err := firstUnexpectedExit("vm startup", watchers); err != nil {
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
	writeBack     func() bool
	once          sync.Once
	err           error
}

func newLaunchSuspendHandler(manager *manager, manifest *manifest.Manifest, qmpSocketPath string, client qmpClient, cid int, notifier notificationSink, writeBack func() bool) *launchSuspendHandler {
	return &launchSuspendHandler{
		manager:       manager,
		manifest:      manifest,
		qmpSocketPath: qmpSocketPath,
		client:        client,
		cid:           cid,
		notifier:      notifier,
		writeBack:     writeBack,
	}
}

func (h *launchSuspendHandler) saveAndExit(ctx context.Context) error {
	h.once.Do(func() {
		if h.writeBack != nil && h.writeBack() {
			if err := h.manager.writeBackGuestFiles(ctx, h.manifest, executor.Group{}); err != nil {
				h.err = err
				return
			}
		}
		if err := h.manager.saveSuspendStateConnected(ctx, h.manifest, h.qmpSocketPath, h.client, h.cid, h.notifier); err != nil {
			h.err = err
			return
		}
		h.err = errSavedSuspendExit
	})
	return h.err
}

func (m *manager) runSSHSession(
	ctx context.Context,
	launchManifest *manifest.Manifest,
	cid int,
	remoteCommand []string,
	stats *launchStats,
	suspendRequests <-chan struct{},
	infoRequests <-chan struct{},
	suspendHandler *launchSuspendHandler,
	guestAgentSocketPath string,
	started *executor.Group,
) error {
	argv := append([]string(nil), launchManifest.SSH.Argv...)
	sessionLogger := m.logger
	if sessionLogger == nil {
		sessionLogger = slog.New(slog.DiscardHandler)
	}
	retryLog := newSSHRetryLogger(sessionLogger)
	provisioned := false

	for {
		stderr := sshtools.NewRetryOutput(m.outputWriter(), false, sshRetryOutputRevealDelay)
		attemptStarted := time.Now()
		stats.MarkSSHAttempt(attemptStarted)
		cmd, err := buildSSHSpecWithArgv(launchManifest, cid, remoteCommand, argv)
		if err != nil {
			return &stageError{Stage: "active session", Err: err}
		}
		sessionLogger.Info("ssh command", "command", shellquote.Join(cmd.Args...))
		cmd.Stderr = stderr
		session, err := m.startManagedProcess(cmd)
		if err != nil {
			return &stageError{Stage: "active session", Err: err}
		}
		watchers := started.Snapshot()
		started.Add(session)
		stats.MarkSSHStarted(attemptStarted)

		err = m.waitForSession(ctx, session, suspendRequests, infoRequests, suspendHandler, guestAgentSocketPath, watchers)
		stderrText := stderr.String()
		if err == nil {
			stderr.Flush()
			return nil
		}
		if sshtools.ClassifyFailure(err, stderrText) == sshtools.FailureTransient {
			stderr.Suppress()
			retryLog.Log(err, stderrText)
			started.Remove(session)
			if waitErr := m.waitBeforeSSHRetry(ctx, launchManifest, suspendRequests, infoRequests, suspendHandler, guestAgentSocketPath, watchers); waitErr != nil {
				return waitErr
			}
			continue
		}
		if launchManifest.SSH.Autoprovision && !provisioned && sshtools.ClassifyFailure(err, stderrText) == sshtools.FailureAuthentication {
			stderr.Suppress()
			started.Remove(session)
			sessionLogger.Info("ssh authentication failed; autoprovisioning a key", "state_dir", launchManifest.ResolvedPersistenceStateDir(), "user", launchManifest.SSH.User)
			key, keyErr := m.ensureSSHAutoprovisionKey(launchManifest)
			if keyErr != nil {
				return &stageError{Stage: "ssh autoprovision", Err: keyErr}
			}
			if installErr := m.installSSHAutoprovisionKey(ctx, launchManifest, key, watchers); installErr != nil {
				return installErr
			}
			sessionLogger.Info("installed autoprovisioned ssh key; retrying ssh", "identity_file", key.IdentityFile, "public_key_file", key.PublicKeyFile)
			argv = (sshtools.Config{Exec: launchManifest.SSH.Argv, User: launchManifest.SSH.User}).WithIdentity(key.IdentityFile).Exec
			provisioned = true
			continue
		}
		stderr.Flush()
		return err
	}
}

func (m *manager) waitBeforeSSHRetry(ctx context.Context, launchManifest *manifest.Manifest, suspendRequests <-chan struct{}, infoRequests <-chan struct{}, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	delay := launchManifest.SSHRetryDelay(m.sshRetryDelay)
	if delay <= 0 {
		delay = m.sshRetryDelay
	}
	if delay <= 0 {
		return nil
	}

	return m.waitForLifecycleEvent(ctx, "active session", nil, delay, suspendRequests, infoRequests, suspendHandler, guestAgentSocketPath, watchers)
}

type sshRetryLogger struct {
	logger            *slog.Logger
	seen              map[sshtools.RetryPhase]bool
	transientFailures int
	warned            bool
}

func newSSHRetryLogger(logger *slog.Logger) *sshRetryLogger {
	return &sshRetryLogger{
		logger: logger,
		seen:   make(map[sshtools.RetryPhase]bool),
	}
}

func (l *sshRetryLogger) Log(err error, stderr string) {
	phase := sshtools.RetryPhaseForFailure(err, stderr)
	if phase == sshtools.RetryPhaseNone {
		return
	}
	l.transientFailures++
	if l.transientFailures == 5 && !l.warned {
		l.warned = true
		l.logger.Warn(
			"ssh exec failed 5 times; ensure the guest is reachable and credentials are configured",
			"ssh_failures",
			l.transientFailures,
		)
	}
	if !l.seen[phase] {
		l.seen[phase] = true
		switch phase {
		case sshtools.RetryPhaseWaiting:
			l.logger.Info("waiting for ssh connection")
		case sshtools.RetryPhaseConnecting:
			l.logger.Info("connecting ssh")
		}
	}
}

func (m *manager) waitForSession(ctx context.Context, session *executor.Process, suspendRequests <-chan struct{}, infoRequests <-chan struct{}, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	if err := m.waitForLifecycleEvent(ctx, "active session", session, 0, suspendRequests, infoRequests, suspendHandler, guestAgentSocketPath, watchers); err != nil {
		return err
	}
	err := session.Wait()
	if err != nil {
		return wrapCommandError("active session", session.Name(), err)
	}
	return nil
}

func (m *manager) waitForVM(ctx context.Context, qemu *executor.Process, suspendRequests <-chan struct{}, infoRequests <-chan struct{}, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	if err := m.waitForLifecycleEvent(ctx, "vm session", qemu, 0, suspendRequests, infoRequests, suspendHandler, guestAgentSocketPath, watchers); err != nil {
		return err
	}
	err := qemu.Wait()
	if err != nil {
		return wrapCommandError("vm session", qemu.Name(), err)
	}
	return nil
}

func (m *manager) waitForLifecycleEvent(ctx context.Context, stage string, process *executor.Process, delay time.Duration, suspendRequests <-chan struct{}, infoRequests <-chan struct{}, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	var processDone <-chan struct{}
	if process != nil {
		processDone = process.Done()
	}
	var delayDone <-chan time.Time
	var timer *time.Timer
	if delay > 0 {
		timer = time.NewTimer(delay)
		delayDone = timer.C
		defer timer.Stop()
	}

	ticker := time.NewTicker(defaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-processDone:
			return nil
		case <-delayDone:
			return nil
		case <-suspendRequests:
			return suspendHandler.saveAndExit(ctx)
		case <-infoRequests:
			m.printGuestInfo(ctx, guestAgentSocketPath, watchers)
		case <-ticker.C:
			if err := firstUnexpectedExit(stage, watchers); err != nil {
				return err
			}
		case <-ctx.Done():
			return &stageError{Stage: stage, Err: ctx.Err()}
		}
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

func firstUnexpectedExit(stage string, watchers executor.Group) error {
	process, err, ok := watchers.FirstExit()
	if !ok {
		return nil
	}
	if err == nil {
		return &stageError{
			Stage: stage,
			Err:   fmt.Errorf("%s exited unexpectedly", process.Name()),
		}
	}
	return wrapCommandError(stage, process.Name(), err)
}

func buildSSHSpec(manifest *manifest.Manifest, cid int, remoteCommand []string) (*exec.Cmd, error) {
	return buildSSHSpecWithArgv(manifest, cid, remoteCommand, manifest.SSH.Argv)
}

func buildSSHSpecWithArgv(launchManifest *manifest.Manifest, cid int, remoteCommand []string, argv []string) (*exec.Cmd, error) {
	renderer, err := manifest.NewTemplateRenderer(manifest.SSHTemplateProvider{
		CID:         cid,
		User:        launchManifest.SSH.User,
		Destination: sshtools.VSockDestination(launchManifest.SSH.User, cid),
	})
	if err != nil {
		return nil, err
	}
	renderedArgv, err := renderer.RenderArgv(argv)
	if err != nil {
		return nil, err
	}
	command, err := sshtools.NewCommand(sshtools.Config{Exec: renderedArgv, User: launchManifest.SSH.User}, cid, remoteCommand)
	if err != nil {
		return nil, err
	}
	cmd := executor.Command(command.Path, command.Args, renderer.Env())
	cmd.Dir = launchManifest.Paths.WorkingDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}

func buildSSHCommandHint(launchManifest *manifest.Manifest, cid int) (string, error) {
	renderer, err := manifest.NewTemplateRenderer(manifest.SSHTemplateProvider{
		CID:         cid,
		User:        launchManifest.SSH.User,
		Destination: sshtools.VSockDestination(launchManifest.SSH.User, cid),
	})
	if err != nil {
		return "", err
	}
	argv, err := renderer.RenderArgv(launchManifest.SSH.Argv)
	if err != nil {
		return "", err
	}
	return sshtools.CommandHint(sshtools.Config{Exec: argv, User: launchManifest.SSH.User}, cid), nil
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

func ensureVolumeImages(volumes []manifest.Volume, logger *slog.Logger) error {
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

		logger.Info("creating volume image", "path", volume.ImagePath, "size_mib", volume.Size, "fs_type", volume.FSType)
		if err := createVolumeImage(volume); err != nil {
			return err
		}
	}

	return nil
}

func createVolumeImage(volume manifest.Volume) error {
	sizeBytes := volume.Size.Bytes()
	file, err := os.OpenFile(volume.ImagePath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create volume image %q: %w", volume.ImagePath, err)
	}

	created := false
	defer func() {
		if !created {
			_ = os.Remove(volume.ImagePath)
		}
	}()

	if err := file.Close(); err != nil {
		return fmt.Errorf("close volume image %q: %w", volume.ImagePath, err)
	}

	if chattrPath, lookErr := exec.LookPath("chattr"); lookErr == nil {
		cmd := exec.Command(chattrPath, "+C", volume.ImagePath)
		_ = cmd.Run()
	}

	if err := os.Truncate(volume.ImagePath, sizeBytes); err != nil {
		return fmt.Errorf("truncate volume image %q: %w", volume.ImagePath, err)
	}

	image, err := backendfile.OpenFromPath(volume.ImagePath, false)
	if err != nil {
		return fmt.Errorf("open volume image %q: %w", volume.ImagePath, err)
	}
	defer image.Close()

	params := &ext4.Params{}
	if volume.Label != "" {
		params.VolumeName = volume.Label
	}
	params.SectorsPerBlock = 8
	fs, err := ext4.Create(image, sizeBytes, 0, int64(ext4.SectorSize512), params)
	if err != nil {
		return fmt.Errorf("format ext4 volume image %q: %w", volume.ImagePath, err)
	}
	if volume.Label == "" {
		if err := fs.SetLabel(""); err != nil {
			return fmt.Errorf("clear default ext4 volume label for %q: %w", volume.ImagePath, err)
		}
	}

	created = true
	return nil
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

func ensureExistingSocketPaths(paths []string) error {
	for _, path := range paths {
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
