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

type launchPlan struct {
	Manifest                    *manifest.Manifest
	RemoteCommand               []string
	Options                     LaunchOptions
	ResumeState                 *suspendState
	Notifier                    notificationSink
	Paths                       RuntimePaths
	VirtioFSSocketPaths         []string
	ExternalVirtioFSSocketPaths []string
	CleanupFiles                []string
	Volumes                     []manifest.Volume
	VolumeImagePaths            []string
	CID                         int
	QEMUCommand                 *exec.Cmd
}

type launchProcesses struct {
	group    executor.Group
	qemu     *executor.Process
	features managedTaskGroup
}

func newLaunchProcesses() *launchProcesses {
	return &launchProcesses{group: executor.NewGroup()}
}

func (p *launchProcesses) Add(processes ...*executor.Process) {
	p.group.Add(processes...)
}

func (p *launchProcesses) AddGroup(group executor.Group) {
	p.group.Add(group.Processes()...)
}

func (p *launchProcesses) SetQEMU(process *executor.Process) {
	p.qemu = process
	p.Add(process)
}

func (p *launchProcesses) QEMU() *executor.Process {
	return p.qemu
}

func (p *launchProcesses) Remove(process *executor.Process) bool {
	return p.group.Remove(process)
}

func (p *launchProcesses) Watchers() executor.Group {
	return p.group.Snapshot()
}

func (p *launchProcesses) VMWatchers() executor.Group {
	watchers := p.Watchers()
	watchers.Remove(p.qemu)
	return watchers
}

func (p *launchProcesses) StartFeatures(ctx context.Context, runtime optionalFeatureRuntime, manifest *manifest.Manifest, client qmpClient) {
	p.features = startOptionalFeatureTasks(ctx, runtime, manifest, client)
}

func (p *launchProcesses) Close(delay time.Duration) error {
	return errors.Join(p.features.Stop(), p.group.StopAll(delay))
}

func (p *launchPlan) RuntimeSocketCleanupFiles() []string {
	paths := make([]string, 0, 4+len(p.CleanupFiles))
	if p.Paths.QMPSocket != "" {
		paths = append(paths, p.Paths.QMPSocket)
	}
	if p.Paths.GuestAgentSocket != "" {
		paths = append(paths, p.Paths.GuestAgentSocket)
	}
	if p.Paths.SSHReadySocket != "" {
		paths = append(paths, p.Paths.SSHReadySocket)
	}
	if p.Paths.ControlSocket != "" {
		paths = append(paths, p.Paths.ControlSocket)
	}
	return append(paths, p.CleanupFiles...)
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

func (m *manager) planLaunch(manifest *manifest.Manifest, remoteCommand []string, options LaunchOptions) (*launchPlan, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if options.SSH && len(remoteCommand) > 0 && len(manifest.SSH.Argv) == 0 {
		return nil, &stageError{Stage: "preflight", Err: fmt.Errorf("remote command arguments require manifest.ssh.exec")}
	}
	resumeMode, err := normalizeResumeMode(options.Resume)
	if err != nil {
		return nil, err
	}
	resumeState, err := resolveLaunchResumeState(manifest, resumeMode)
	if err != nil {
		return nil, err
	}
	virtioFSSocketPaths, err := manifest.ResolvedVirtioFSSocketPaths()
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}
	externalVirtioFSSocketPaths, err := manifest.ResolvedExternalVirtioFSSocketPaths()
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}
	cleanupFiles, err := manifest.ResolvedCleanupFiles()
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}
	qmpSocketPath, err := manifest.ResolvedQMPSocketPath()
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}
	guestAgentSocketPath, err := manifest.ResolvedGuestAgentSocketPath()
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}
	sshReadySocketPath, err := manifest.ResolvedSSHReadySocketPath()
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}
	controlSocketPath, err := manifest.ResolvedControlSocketPath()
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}
	volumes := manifest.ResolvedVolumes()
	volumeImagePaths := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		volumeImagePaths = append(volumeImagePaths, volume.ImagePath)
	}
	return &launchPlan{
		Manifest:                    manifest,
		RemoteCommand:               append([]string(nil), remoteCommand...),
		Options:                     options,
		ResumeState:                 resumeState,
		Notifier:                    m.effectiveNotifier(manifest),
		Paths:                       RuntimePaths{StateDir: manifest.ResolvedPersistenceStateDir(), ControlSocket: controlSocketPath, QMPSocket: qmpSocketPath, GuestAgentSocket: guestAgentSocketPath, SSHReadySocket: sshReadySocketPath, Cleanup: append([]string(nil), cleanupFiles...)},
		VirtioFSSocketPaths:         virtioFSSocketPaths,
		ExternalVirtioFSSocketPaths: externalVirtioFSSocketPaths,
		CleanupFiles:                cleanupFiles,
		Volumes:                     volumes,
		VolumeImagePaths:            volumeImagePaths,
	}, nil
}

func (m *manager) finalizeLockedLaunchPlan(plan *launchPlan) error {
	cid, err := m.acquireLaunchCID(plan.Manifest, plan.ResumeState)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	qemuCmd, err := buildQEMUCommand(plan.Manifest, cid, plan.ResumeState != nil)
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	plan.CID = cid
	plan.QEMUCommand = qemuCmd
	return nil
}

func (m *manager) launchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options LaunchOptions) (err error) {
	stats := newLaunchStats(time.Now())
	plan, err := m.planLaunch(manifest, remoteCommand, options)
	if err != nil {
		return err
	}

	launchCtx, cancelLaunch := context.WithCancel(ctx)
	defer cancelLaunch()
	lifecycle := m.startLaunchLifecycle(cancelLaunch)
	defer lifecycle.Stop()

	lock, err := m.locker.Acquire(manifest.ResolvedLockPath())
	if err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, lock.Release)

	if plan.ResumeState == nil {
		if err := removeSuspendState(manifest); err != nil {
			return &stageError{Stage: "preflight", Err: err}
		}
	}
	if plan.ResumeState != nil {
		// Recheck after acquiring the launch lock so restore does not race a
		// concurrent launch/suspend cleanup.
		if _, err := os.Stat(plan.ResumeState.VMStatePath); err != nil {
			return &stageError{Stage: "preflight", Err: fmt.Errorf("saved vm state %q is not available: %w", plan.ResumeState.VMStatePath, err)}
		}
	}
	if err := writeLaunchPID(manifest, os.Getpid()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	defer joinDeferredError(&err, func() error {
		return removeLaunchPID(manifest, os.Getpid())
	})

	if err := m.finalizeLockedLaunchPlan(plan); err != nil {
		return err
	}
	if plan.ResumeState != nil {
		m.logger.Info("restoring saved vsock cid", "cid", plan.CID)
	} else {
		m.logger.Info("allocated vsock cid", "cid", plan.CID)
	}

	if err := m.prepareLaunchFilesystem(plan); err != nil {
		return err
	}

	processes := newLaunchProcesses()
	var qmpClient qmpClient
	var runtime *Runtime
	writeBackOnExit := false
	defer func() {
		savedSuspend := errors.Is(err, errSavedSuspendExit)
		if savedSuspend {
			err = nil
		}
		if writeBackOnExit && !savedSuspend {
			writeBackCtx, cancelWriteBack := context.WithTimeout(context.Background(), m.effectiveQMPCommandTimeout())
			writeBackErr := m.writeBackGuestFiles(writeBackCtx, plan.Manifest, executor.Group{})
			cancelWriteBack()
			if writeBackErr != nil {
				err = errors.Join(err, writeBackErr)
			}
		}
		var runtimeErr error
		if runtime != nil {
			runtimeErr = runtime.Close()
		}
		processErr := processes.Close(m.shutdownDelay)
		var disconnectErr error
		if qmpClient != nil {
			disconnectErr = qmpClient.Disconnect()
		}
		cleanupErr := removeSocketPaths(plan.RuntimeSocketCleanupFiles())
		if err == nil {
			err = errors.Join(runtimeErr, processErr, disconnectErr, cleanupErr)
		} else if runtimeErr != nil || processErr != nil || disconnectErr != nil || cleanupErr != nil {
			err = errors.Join(err, runtimeErr, processErr, disconnectErr, cleanupErr)
		}
		stats.MarkCompleted(time.Now())
		fmt.Fprintf(m.outputWriter(), "stats: %s\n", stats.String())
	}()

	runtime, qmpClient, err = m.startLaunchRuntime(launchCtx, plan, stats, lifecycle, processes)
	if err != nil {
		return err
	}
	if plan.ResumeState != nil {
		if err := m.restoreLaunchRuntime(launchCtx, plan, qmpClient); err != nil {
			return err
		}
		writeBackOnExit = true
	}
	suspendHandler := newLaunchSuspendHandler(m, plan.Manifest, plan.Paths.QMPSocket, qmpClient, plan.CID, plan.Notifier, func() bool {
		return writeBackOnExit
	})
	runtime.SetReady()
	if err := runtime.StartControl(launchCtx); err != nil {
		return &stageError{Stage: "control startup", Err: err}
	}
	// Honor a suspend signal queued during startup before guest-file install or
	// SSH startup proceeds.
	select {
	case <-lifecycle.Suspend().Notify():
		return handleSuspendRequest(launchCtx, lifecycle.Suspend(), suspendHandler)
	default:
	}

	if plan.ResumeState == nil {
		if err := m.writeGuestFiles(launchCtx, plan.Manifest, stats, processes.Watchers()); err != nil {
			return err
		}
		writeBackOnExit = true
		stats.MarkFilesReady(time.Now())

		if plan.Paths.SSHReadySocket != "" {
			m.logger.Info("waiting for ssh readiness")
			if err := m.waitForSSHReady(launchCtx, plan.Paths.SSHReadySocket, processes.Watchers()); err != nil {
				return err
			}
		}
		stats.MarkSSHReady(time.Now())
	}

	if plan.Options.SSH && len(plan.Manifest.SSH.Argv) > 0 {
		processes.StartFeatures(launchCtx, optionalFeatureRuntime{
			qmpTimeout: m.effectiveQMPCommandTimeout(),
			notifier:   plan.Notifier,
		}, plan.Manifest, qmpClient)

		if err := m.runSSHSession(launchCtx, plan, stats, lifecycle, suspendHandler, processes); err != nil {
			return err
		}
		if plan.ResumeState != nil {
			if err := removeRestoredSuspendState(plan); err != nil {
				return err
			}
		}
		return nil
	}

	processes.StartFeatures(launchCtx, optionalFeatureRuntime{
		qmpTimeout: m.effectiveQMPCommandTimeout(),
		notifier:   plan.Notifier,
	}, plan.Manifest, qmpClient)

	if plan.ResumeState != nil {
		if err := removeRestoredSuspendState(plan); err != nil {
			return err
		}
	}

	hint, err := buildSSHCommandHint(plan.Manifest, plan.CID)
	if err != nil {
		m.logger.Info("ssh command hint template failed", "err", err)
	} else if hint != "" {
		fmt.Fprintf(m.outputWriter(), "connect with: %s\n", hint)
	}
	vmWatchers := processes.VMWatchers()
	runtime.SetWatchers(vmWatchers)
	if err := m.waitForVM(launchCtx, processes.QEMU(), lifecycle, suspendHandler, plan.Paths.GuestAgentSocket, vmWatchers); err != nil {
		return err
	}
	return nil
}

func (m *manager) prepareLaunchFilesystem(plan *launchPlan) error {
	if err := ensureDirectories(plan.Manifest.ResolvedPersistenceDirectories()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(plan.RuntimeSocketCleanupFiles()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureExistingSocketPaths(plan.ExternalVirtioFSSocketPaths); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureParentDirectories(plan.VolumeImagePaths); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := removeSocketPaths(plan.RuntimeSocketCleanupFiles()); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	if err := ensureVolumeImages(plan.Volumes, m.logger); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	return nil
}

func (m *manager) startLaunchRuntime(ctx context.Context, plan *launchPlan, stats *launchStats, lifecycle *launchLifecycle, processes *launchProcesses) (*Runtime, qmpClient, error) {
	runProcesses, err := m.startRuns(plan.CID, plan.Manifest)
	if err != nil {
		return nil, nil, err
	}
	processes.AddGroup(runProcesses)

	if len(plan.VirtioFSSocketPaths) > 0 {
		m.logger.Info("waiting for virtiofs sockets")
		if err := m.waitForSockets(ctx, "virtiofs startup", plan.VirtioFSSocketPaths, processes.Watchers()); err != nil {
			return nil, nil, err
		}
	}

	if plan.ResumeState != nil {
		m.logger.Info("starting qemu for restore")
	} else {
		m.logger.Info("starting qemu")
	}
	stats.MarkBootStarted(time.Now())
	qemu, err := m.startManagedProcess(plan.QEMUCommand)
	if err != nil {
		return nil, nil, &stageError{Stage: "vm startup", Err: err}
	}
	processes.SetQEMU(qemu)

	m.logger.Info("waiting for qmp readiness")
	client, err := m.waitForQMP(ctx, plan.Paths.QMPSocket, processes.Watchers())
	if err != nil {
		return nil, nil, err
	}
	runtime := newRuntime(m, plan.Manifest, plan.Paths, plan.CID, stats, client, lifecycle.Suspend())
	client = runtime.QMP()
	stats.MarkQMPReady(time.Now())
	qemu.SetShutdown(func() error {
		return client.Quit(m.effectiveQMPQuitTimeout())
	})
	return runtime, client, nil
}

func (m *manager) restoreLaunchRuntime(ctx context.Context, plan *launchPlan, client qmpClient) error {
	m.logger.Info("restoring vm state", "path", plan.ResumeState.VMStatePath)
	if err := client.MigrateIncoming(m.effectiveQMPMigrationTimeout(), plan.ResumeState.VMStatePath); err != nil {
		return &stageError{Stage: "restore", Err: err}
	}
	if err := m.waitForMigration(ctx, client); err != nil {
		return &stageError{Stage: "restore", Err: err}
	}
	if err := client.Cont(m.effectiveQMPCommandTimeout()); err != nil {
		return &stageError{Stage: "restore", Err: err}
	}
	plan.Notifier.Notify(ctx, notifyStateRuntimeResume, "Restored saved VM state", map[string]string{
		"host_name":     plan.Manifest.Identity.HostName,
		"vm_state_path": plan.ResumeState.VMStatePath,
		"cid":           fmt.Sprintf("%d", plan.CID),
	})
	return nil
}

func removeRestoredSuspendState(plan *launchPlan) error {
	if err := os.Remove(plan.ResumeState.VMStatePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &stageError{Stage: "restore", Err: fmt.Errorf("remove saved vm state %q: %w", plan.ResumeState.VMStatePath, err)}
	}
	if err := removeSuspendState(plan.Manifest); err != nil {
		return &stageError{Stage: "restore", Err: err}
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

type launchSuspendCoordinator struct {
	mu        sync.Mutex
	notify    chan struct{}
	waiters   []chan error
	requested bool
	inFlight  bool
	completed bool
	result    error
}

type launchLifecycle struct {
	suspend    *launchSuspendCoordinator
	info       chan struct{}
	signalDone chan struct{}
	stopSignal func()
	stopOnce   sync.Once
}

func (m *manager) startLaunchLifecycle(cancel context.CancelFunc) *launchLifecycle {
	signalCh, stopSignals := m.launchSignalChannel()
	lifecycle := &launchLifecycle{
		suspend:    newLaunchSuspendCoordinator(),
		info:       make(chan struct{}, 1),
		signalDone: make(chan struct{}),
		stopSignal: stopSignals,
	}
	go lifecycle.watchSignals(signalCh, cancel)
	return lifecycle
}

func (l *launchLifecycle) watchSignals(signalCh <-chan os.Signal, cancel context.CancelFunc) {
	for {
		select {
		case <-l.signalDone:
			return
		case sig, ok := <-signalCh:
			if !ok {
				return
			}
			switch sig {
			case os.Interrupt, syscall.SIGTERM:
				cancel()
			case syscall.SIGTSTP:
				l.Suspend().Request()
			case syscall.SIGUSR1:
				l.RequestInfo()
			}
		}
	}
}

func (l *launchLifecycle) Stop() {
	l.stopOnce.Do(func() {
		close(l.signalDone)
		l.stopSignal()
	})
}

func (l *launchLifecycle) Suspend() *launchSuspendCoordinator {
	return l.suspend
}

func (l *launchLifecycle) Info() <-chan struct{} {
	return l.info
}

func (l *launchLifecycle) RequestInfo() {
	select {
	case l.info <- struct{}{}:
	default:
	}
}

func newLaunchSuspendCoordinator() *launchSuspendCoordinator {
	return &launchSuspendCoordinator{notify: make(chan struct{}, 1)}
}

func (c *launchSuspendCoordinator) Notify() <-chan struct{} {
	return c.notify
}

func (c *launchSuspendCoordinator) Request() {
	c.request(nil)
}

func (c *launchSuspendCoordinator) RequestAndWait(ctx context.Context) error {
	done := make(chan error, 1)
	c.request(done)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *launchSuspendCoordinator) request(done chan error) {
	c.mu.Lock()
	if c.completed {
		result := c.result
		c.mu.Unlock()
		if done != nil {
			done <- result
		}
		return
	}
	if done != nil {
		c.waiters = append(c.waiters, done)
	}
	notify := false
	if !c.requested && !c.inFlight {
		c.requested = true
		notify = true
	}
	c.mu.Unlock()

	if notify {
		select {
		case c.notify <- struct{}{}:
		default:
		}
	}
}

func (c *launchSuspendCoordinator) Begin() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requested = false
	c.inFlight = true
}

func (c *launchSuspendCoordinator) Complete(err error) {
	c.mu.Lock()
	c.inFlight = false
	c.completed = true
	c.result = err
	waiters := c.waiters
	c.waiters = nil
	c.mu.Unlock()

	for _, waiter := range waiters {
		waiter <- err
	}
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

func handleSuspendRequest(ctx context.Context, coordinator *launchSuspendCoordinator, handler *launchSuspendHandler) error {
	coordinator.Begin()
	err := handler.saveAndExit(ctx)
	coordinator.Complete(err)
	return err
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
	plan *launchPlan,
	stats *launchStats,
	lifecycle *launchLifecycle,
	suspendHandler *launchSuspendHandler,
	processes *launchProcesses,
) error {
	launchManifest := plan.Manifest
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
		cmd, err := buildSSHSpecWithArgv(launchManifest, plan.CID, plan.RemoteCommand, argv)
		if err != nil {
			return &stageError{Stage: "active session", Err: err}
		}
		sessionLogger.Info("ssh command", "command", shellquote.Join(cmd.Args...))
		cmd.Stderr = stderr
		session, err := m.startManagedProcess(cmd)
		if err != nil {
			return &stageError{Stage: "active session", Err: err}
		}
		watchers := processes.Watchers()
		processes.Add(session)
		stats.MarkSSHStarted(attemptStarted)

		err = m.waitForSession(ctx, session, lifecycle, suspendHandler, plan.Paths.GuestAgentSocket, watchers)
		stderrText := stderr.String()
		if err == nil {
			stderr.Flush()
			return nil
		}
		if sshtools.ClassifyFailure(err, stderrText) == sshtools.FailureTransient {
			stderr.Suppress()
			retryLog.Log(err, stderrText)
			processes.Remove(session)
			if waitErr := m.waitBeforeSSHRetry(ctx, launchManifest, lifecycle, suspendHandler, plan.Paths.GuestAgentSocket, watchers); waitErr != nil {
				return waitErr
			}
			continue
		}
		if launchManifest.SSH.Autoprovision && !provisioned && sshtools.ClassifyFailure(err, stderrText) == sshtools.FailureAuthentication {
			stderr.Suppress()
			processes.Remove(session)
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

func (m *manager) waitBeforeSSHRetry(ctx context.Context, launchManifest *manifest.Manifest, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	delay := launchManifest.SSHRetryDelay(m.sshRetryDelay)
	if delay <= 0 {
		delay = m.sshRetryDelay
	}
	if delay <= 0 {
		return nil
	}

	return m.waitForLifecycleEvent(ctx, "active session", nil, delay, lifecycle, suspendHandler, guestAgentSocketPath, watchers)
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

func (m *manager) waitForSession(ctx context.Context, session *executor.Process, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	if err := m.waitForLifecycleEvent(ctx, "active session", session, 0, lifecycle, suspendHandler, guestAgentSocketPath, watchers); err != nil {
		return err
	}
	err := session.Wait()
	if err != nil {
		return wrapCommandError("active session", session.Name(), err)
	}
	return nil
}

func (m *manager) waitForVM(ctx context.Context, qemu *executor.Process, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	if err := m.waitForLifecycleEvent(ctx, "vm session", qemu, 0, lifecycle, suspendHandler, guestAgentSocketPath, watchers); err != nil {
		return err
	}
	err := qemu.Wait()
	if err != nil {
		return wrapCommandError("vm session", qemu.Name(), err)
	}
	return nil
}

func (m *manager) waitForLifecycleEvent(ctx context.Context, stage string, process *executor.Process, delay time.Duration, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
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
		case <-lifecycle.Suspend().Notify():
			return handleSuspendRequest(ctx, lifecycle.Suspend(), suspendHandler)
		case <-lifecycle.Info():
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
