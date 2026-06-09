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
	"sync"
	"syscall"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

const (
	defaultSSHRetryDelay      = 500 * time.Millisecond
	defaultShutdownDelay      = 15 * time.Second
	defaultMigrationPollDelay = 100 * time.Millisecond
	sshRetryOutputRevealDelay = 250 * time.Millisecond
)

var errSavedSuspendExit = errors.New("saved suspend requested")

type ResumeMode = launch.ResumeMode

const (
	ResumeModeNo    = launch.ResumeModeNo
	ResumeModeAuto  = launch.ResumeModeAuto
	ResumeModeForce = launch.ResumeModeForce
)

type LaunchOptions = launch.Options

type WaitMode = launch.WaitMode

const (
	WaitAuto = launch.WaitAuto
	WaitSSH  = launch.WaitSSH
	WaitVM   = launch.WaitVM
)

type LaunchSpec = launch.Spec
type Plan = launch.Plan
type RuntimePaths = launch.RuntimePaths
type suspendState = launch.SuspendState
type notificationSink = launch.NotificationSink
type launchLifecycle = launch.Lifecycle
type launchSuspendCoordinator = launch.SuspendCoordinator
type Config = launch.Config

var newLaunchSuspendCoordinator = launch.NewSuspendCoordinator

type Launcher struct {
	manager *manager
}

func DefaultConfig() Config {
	return Config{
		Locker:              &fileLocker{},
		VSockCIDChecker:     newHostVSockCIDChecker(),
		Runner:              &executor.Runner{},
		SocketWaiter:        &pollingSocketWaiter{},
		QMPDialer:           &socketMonitorDialer{},
		GuestAgentDialer:    &socketGuestAgentDialer{},
		SSHReadyDialer:      &unixSSHReadyDialer{},
		Logger:              logger,
		LogWriter:           os.Stderr,
		SSHRetryDelay:       defaultSSHRetryDelay,
		SSHReadyTimeout:     configuredSSHReadyTimeout(),
		ShutdownDelay:       defaultShutdownDelay,
		QMPRetryDelay:       defaultQMPRetryDelay,
		QMPConnectTimeout:   defaultQMPConnectTimeout,
		QMPQuitTimeout:      defaultQMPQuitTimeout,
		QMPMigrationTimeout: defaultQMPMigrationTimeout,
	}
}

func NewLauncher(configs ...Config) *Launcher {
	config := DefaultConfig()
	if len(configs) > 0 {
		config = launch.MergeConfig(config, configs[0])
	}
	return &Launcher{manager: newManagerFromConfig(config)}
}

func (l *Launcher) Plan(ctx context.Context, spec LaunchSpec) (*Plan, error) {
	_ = ctx
	if l == nil || l.manager == nil {
		l = NewLauncher()
	}
	return l.manager.planLaunch(spec)
}

func (l *Launcher) Start(ctx context.Context, plan *Plan) (*Runtime, error) {
	if l == nil || l.manager == nil {
		l = NewLauncher()
	}
	return l.manager.startWithPlan(ctx, plan)
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
	return newManagerFromConfig(DefaultConfig())
}

func newManagerFromConfig(config Config) *manager {
	config = launch.MergeConfig(DefaultConfig(), config)
	return &manager{
		locker:              config.Locker,
		vsockCIDChecker:     config.VSockCIDChecker,
		runner:              config.Runner,
		socketWaiter:        config.SocketWaiter,
		qmpDialer:           config.QMPDialer,
		guestAgentDialer:    config.GuestAgentDialer,
		sshReadyDialer:      config.SSHReadyDialer,
		logger:              config.Logger,
		logWriter:           config.LogWriter,
		sshRetryDelay:       config.SSHRetryDelay,
		sshReadyTimeout:     config.SSHReadyTimeout,
		shutdownDelay:       config.ShutdownDelay,
		qmpRetryDelay:       config.QMPRetryDelay,
		qmpConnectTimeout:   config.QMPConnectTimeout,
		qmpQuitTimeout:      config.QMPQuitTimeout,
		qmpMigrationTimeout: config.QMPMigrationTimeout,
		signals:             config.Signals,
		pidSignaler:         config.PIDSignaler,
		notifier:            config.Notifier,
	}
}

// Launch runs the supported virtie sandbox session.
func Launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) error {
	return NewLauncher().launch(ctx, manifest, remoteCommand)
}

// LaunchWithOptions runs the supported virtie sandbox session with explicit launch options.
func LaunchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options LaunchOptions) error {
	return NewLauncher().launchWithOptions(ctx, manifest, remoteCommand, options)
}

func (l *Launcher) launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) (err error) {
	return l.launchWithOptions(ctx, manifest, remoteCommand, LaunchOptions{Resume: ResumeModeNo, SSH: true})
}

func (l *Launcher) launchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options LaunchOptions) error {
	plan, err := l.Plan(ctx, LaunchSpec{Manifest: manifest, RemoteCommand: remoteCommand, Options: options})
	if err != nil {
		return err
	}
	return l.manager.launchWithPlan(ctx, plan)
}

func (m *manager) launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) error {
	return m.launchWithOptions(ctx, manifest, remoteCommand, LaunchOptions{Resume: ResumeModeNo, SSH: true})
}

func (m *manager) launchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options LaunchOptions) error {
	plan, err := m.planLaunch(LaunchSpec{Manifest: manifest, RemoteCommand: remoteCommand, Options: options})
	if err != nil {
		return err
	}
	return m.launchWithPlan(ctx, plan)
}

func (m *manager) planLaunch(spec LaunchSpec) (*Plan, error) {
	manifest := spec.Manifest
	options := spec.Options
	resumeMode, err := launch.NormalizeResumeMode(options.Resume)
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}
	resumeState, err := resolveLaunchResumeState(manifest, resumeMode)
	if err != nil {
		return nil, err
	}
	plan, err := launch.BuildPlan(spec, resumeState, m.effectiveNotifier(manifest))
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}
	return plan, nil
}

func (m *manager) finalizeLockedLaunchPlan(plan *Plan) error {
	if err := launch.FinalizeLockedPlan(plan, m.vsockCIDChecker, buildQEMUCommand); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	return nil
}

func (m *manager) launchWithPlan(ctx context.Context, plan *Plan) (err error) {
	runtime, err := m.startWithPlan(ctx, plan)
	if err != nil {
		if errors.Is(err, errSavedSuspendExit) {
			return nil
		}
		return err
	}
	defer joinDeferredError(&err, runtime.Close)
	err = runtime.Wait(ctx, plan.Options.WaitMode())
	if errors.Is(err, errSavedSuspendExit) {
		return nil
	}
	return err
}

func (m *manager) startWithPlan(ctx context.Context, plan *Plan) (runtime *Runtime, err error) {
	stats := newLaunchStats(time.Now())
	manifest := plan.Manifest

	launchCtx, cancelLaunch := context.WithCancel(ctx)
	lifecycle := m.startLaunchLifecycle(cancelLaunch)

	runtimeLock, err := launch.AcquireRuntimeLock(launch.RuntimeLockSpec{
		Manifest:    manifest,
		ResumeState: plan.ResumeState,
		Locker:      m.locker,
		Lifecycle:   lifecycle,
		Cancel:      cancelLaunch,
		PID:         os.Getpid(),
	})
	if err != nil {
		return nil, &stageError{Stage: "preflight", Err: err}
	}

	cleanupRuntime := func() error {
		return runtimeLock.Cleanup()
	}

	if err := m.finalizeLockedLaunchPlan(plan); err != nil {
		_ = cleanupRuntime()
		return nil, err
	}
	if plan.ResumeState != nil {
		m.logger.Info("restoring saved vsock cid", "cid", plan.CID)
	} else {
		m.logger.Info("allocated vsock cid", "cid", plan.CID)
	}

	if err := m.prepareLaunchFilesystem(plan); err != nil {
		_ = cleanupRuntime()
		return nil, err
	}

	processes := newProcessSet()
	var qmpClient qmpClient
	writeBackOnExit := false
	defer func() {
		if err != nil {
			var runtimeErr error
			if runtime != nil {
				if errors.Is(err, errSavedSuspendExit) {
					runtime.savedSuspend = true
				}
				runtimeErr = runtime.Close()
			} else {
				runtimeErr = errors.Join(processes.Close(m.shutdownDelay), cleanupRuntime())
				if qmpClient != nil {
					runtimeErr = errors.Join(runtimeErr, qmpClient.Disconnect())
				}
				runtimeErr = errors.Join(runtimeErr, launch.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles()))
				stats.MarkCompleted(time.Now())
				fmt.Fprintf(m.outputWriter(), "stats: %s\n", stats.String())
			}
			err = errors.Join(err, runtimeErr)
		}
	}()

	runtime, qmpClient, err = m.startLaunchRuntime(launchCtx, plan, stats, lifecycle, processes)
	if err != nil {
		return nil, err
	}
	if plan.ResumeState != nil {
		if err := m.restoreLaunchRuntime(launchCtx, plan, qmpClient); err != nil {
			return nil, err
		}
		writeBackOnExit = true
	}
	suspendHandler := newLaunchSuspendHandler(m, plan.Manifest, plan.Paths.QMPSocket, qmpClient, plan.CID, plan.Notifier, func() bool {
		return writeBackOnExit
	})
	runtime.SetReady()
	runtime.SetLaunchLifecycle(plan, lifecycle, suspendHandler)
	runtime.SetCloseHooks(runtimeCloseHooks{
		WriteBack: func(ctx context.Context) error {
			if !writeBackOnExit {
				return nil
			}
			return m.writeBackGuestFiles(ctx, plan.Manifest, executor.Group{})
		},
		Cleanup: func() error {
			return errors.Join(launch.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles()), cleanupRuntime())
		},
		Stats: func() {
			stats.MarkCompleted(time.Now())
			fmt.Fprintf(m.outputWriter(), "stats: %s\n", stats.String())
		},
	})
	if err := runtime.StartControl(launchCtx); err != nil {
		return nil, &stageError{Stage: "control startup", Err: err}
	}
	// Honor a suspend signal queued during startup before guest-file install or
	// SSH startup proceeds.
	if err := launch.HandleQueuedSuspend(launchCtx, lifecycle, func(ctx context.Context, coordinator *launchSuspendCoordinator) error {
		return handleSuspendRequest(ctx, coordinator, suspendHandler)
	}); err != nil {
		return nil, err
	}

	if plan.ResumeState == nil {
		if err := m.writeGuestFiles(launchCtx, plan.Manifest, stats, processes.Watchers()); err != nil {
			return nil, err
		}
		writeBackOnExit = true
		stats.MarkFilesReady(time.Now())

		if plan.Paths.SSHReadySocket != "" {
			m.logger.Info("waiting for ssh readiness")
			if err := m.waitForSSHReady(launchCtx, plan.Paths.SSHReadySocket, processes.Watchers()); err != nil {
				return nil, err
			}
		}
		stats.MarkSSHReady(time.Now())
	}

	return runtime, nil
}

func (m *manager) prepareLaunchFilesystem(plan *Plan) error {
	if err := launch.PrepareFilesystem(plan, m.logger); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	return nil
}

func (m *manager) startLaunchRuntime(ctx context.Context, plan *Plan, stats *launchStats, lifecycle *launchLifecycle, processes *ProcessSet) (*Runtime, qmpClient, error) {
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

	stats.MarkBootStarted(time.Now())
	qemu, err := launch.StartQEMU(m.runner, m.logger, plan)
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
	runtime.SetProcesses(processes, m.shutdownDelay)
	client = runtime.QMP()
	stats.MarkQMPReady(time.Now())
	qemu.SetShutdown(func() error {
		return client.Quit(m.effectiveQMPQuitTimeout())
	})
	return runtime, client, nil
}

func (m *manager) restoreLaunchRuntime(ctx context.Context, plan *Plan, client qmpClient) error {
	m.logger.Info("restoring vm state", "path", plan.ResumeState.VMStatePath)
	if err := qmpclient.RestoreFromFile(ctx, client, plan.ResumeState.VMStatePath, qmpclient.RestoreWait{
		MigrationTimeout: m.effectiveQMPMigrationTimeout(),
		CommandTimeout:   m.effectiveQMPCommandTimeout(),
		PollDelay:        defaultMigrationPollDelay,
	}); err != nil {
		return &stageError{Stage: "restore", Err: err}
	}
	plan.Notifier.Notify(ctx, notifyStateRuntimeResume, "Restored saved VM state", map[string]string{
		"host_name":     plan.Manifest.Identity.HostName,
		"vm_state_path": plan.ResumeState.VMStatePath,
		"cid":           fmt.Sprintf("%d", plan.CID),
	})
	return nil
}

func removeRestoredSuspendState(plan *Plan) error {
	if err := launch.RemoveRestoredSuspendState(plan); err != nil {
		return &stageError{Stage: "restore", Err: err}
	}
	return nil
}

func (m *manager) waitForLaunchForeground(
	ctx context.Context,
	plan *Plan,
	stats *launchStats,
	runtime *Runtime,
	qmpClient qmpClient,
	lifecycle *launchLifecycle,
	suspendHandler *launchSuspendHandler,
	processes *ProcessSet,
) error {
	processes.SetFeatures(startOptionalFeatureTasks(ctx, optionalFeatureRuntime{
		qmpTimeout: m.effectiveQMPCommandTimeout(),
		notifier:   plan.Notifier,
	}, plan.Manifest, qmpClient))

	if plan.Options.SSH && len(plan.Manifest.SSH.Argv) > 0 {
		if err := m.runSSHSession(ctx, plan, stats, lifecycle, suspendHandler, processes); err != nil {
			return err
		}
		if plan.ResumeState != nil {
			return removeRestoredSuspendState(plan)
		}
		return nil
	}

	if plan.ResumeState != nil {
		if err := removeRestoredSuspendState(plan); err != nil {
			return err
		}
	}

	hint, err := launch.BuildSSHCommandHint(plan.Manifest, plan.CID)
	if err != nil {
		m.logger.Info("ssh command hint template failed", "err", err)
	} else if hint != "" {
		fmt.Fprintf(m.outputWriter(), "connect with: %s\n", hint)
	}
	vmWatchers := processes.VMWatchers()
	runtime.SetWatchers(vmWatchers)
	return m.waitForVM(ctx, processes.QEMU(), lifecycle, suspendHandler, plan.Paths.GuestAgentSocket, vmWatchers)
}

func resolveLaunchResumeState(manifest *manifest.Manifest, mode ResumeMode) (*suspendState, error) {
	state, err := launch.ResolveResumeState(manifest, mode)
	if err != nil {
		return nil, &stageError{Stage: "restore", Err: err}
	}
	return state, nil
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
	runs, err := launch.StartRuns(launch.RunStarter{
		Runner:        m.runner,
		Logger:        m.logger,
		ShutdownDelay: m.shutdownDelay,
	}, cid, manifest)
	if err != nil {
		return executor.Group{}, &stageError{Stage: "run startup", Err: err}
	}
	return runs, nil
}

func (m *manager) waitForSockets(ctx context.Context, stage string, socketPaths []string, watchers executor.Group) error {
	return m.waitForAsyncStage(ctx, stage, watchers, func(waitCtx context.Context) error {
		return m.socketWaiter.Wait(waitCtx, socketPaths)
	})
}

func (m *manager) waitForQMP(ctx context.Context, socketPath string, watchers executor.Group) (qmpClient, error) {
	dialer := m.qmpDialer
	if dialer == nil {
		dialer = &socketMonitorDialer{}
	}
	retryDelay := m.qmpRetryDelay
	if retryDelay <= 0 {
		retryDelay = defaultQMPRetryDelay
	}
	return launch.WaitForQMP(ctx, launch.QMPWait{
		Stage:          "vm startup",
		SocketPath:     socketPath,
		SocketWaiter:   m.socketWaiter,
		Dialer:         dialer,
		ConnectTimeout: m.effectiveQMPConnectTimeout(),
		RetryDelay:     retryDelay,
		PollDelay:      defaultSocketPollInterval,
		Check: func(stage string) error {
			return firstUnexpectedExit(stage, watchers)
		},
		Result: func(stage string, err error) error {
			return &stageError{Stage: stage, Err: err}
		},
		Cancel: func(stage string, err error) error {
			return &stageError{Stage: stage, Err: err}
		},
	})
}

func (m *manager) waitForAsyncStage(ctx context.Context, stage string, watchers executor.Group, wait func(context.Context) error) error {
	return launch.WaitForAsync(ctx, launch.AsyncWait{
		Stage:     stage,
		PollDelay: defaultSocketPollInterval,
		Wait:      wait,
		Check: func(stage string) error {
			return firstUnexpectedExit(stage, watchers)
		},
		Result: func(stage string, err error) error {
			return &stageError{Stage: stage, Err: err}
		},
		Cancel: func(stage string, err error) error {
			return &stageError{Stage: stage, Err: err}
		},
	})
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

func (m *manager) startLaunchLifecycle(cancel context.CancelFunc) *launchLifecycle {
	signalCh, stopSignals := m.launchSignalChannel()
	return launch.NewLifecycle(signalCh, stopSignals, cancel)
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
	plan *Plan,
	stats *launchStats,
	lifecycle *launchLifecycle,
	suspendHandler *launchSuspendHandler,
	processes *ProcessSet,
) error {
	return launch.RunSSHSession(ctx, launch.SSHSession{
		Plan:                   plan,
		Runner:                 m.runner,
		Processes:              processes,
		Stats:                  stats,
		Logger:                 m.logger,
		Output:                 m.outputWriter(),
		RetryOutputRevealDelay: sshRetryOutputRevealDelay,
		Wait: func(ctx context.Context, session *executor.Process, watchers executor.Group) error {
			return m.waitForSession(ctx, session, lifecycle, suspendHandler, plan.Paths.GuestAgentSocket, watchers)
		},
		WaitForRetry: func(ctx context.Context, watchers executor.Group) error {
			return m.waitBeforeSSHRetry(ctx, plan.Manifest, lifecycle, suspendHandler, plan.Paths.GuestAgentSocket, watchers)
		},
		EnsureKey: func(launchManifest *manifest.Manifest) (launch.SSHAutoprovisionKey, error) {
			key, err := m.ensureSSHAutoprovisionKey(launchManifest)
			if err != nil {
				return launch.SSHAutoprovisionKey{}, err
			}
			return launch.SSHAutoprovisionKey{
				IdentityFile:  key.IdentityFile,
				PublicKeyFile: key.PublicKeyFile,
				AuthorizedKey: key.AuthorizedKey,
			}, nil
		},
		InstallKey: func(ctx context.Context, launchManifest *manifest.Manifest, key launch.SSHAutoprovisionKey, watchers executor.Group) error {
			return m.installSSHAutoprovisionKey(ctx, launchManifest, sshAutoprovisionKey{
				IdentityFile:  key.IdentityFile,
				PublicKeyFile: key.PublicKeyFile,
				AuthorizedKey: key.AuthorizedKey,
			}, watchers)
		},
		WrapStage: func(stage string, err error) error {
			return &stageError{Stage: stage, Err: err}
		},
	})
}

func (m *manager) waitBeforeSSHRetry(ctx context.Context, launchManifest *manifest.Manifest, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	delay := launchManifest.SSHRetryDelay(m.sshRetryDelay)
	if delay <= 0 {
		delay = m.sshRetryDelay
	}
	if delay <= 0 {
		return nil
	}

	return m.waitForLifecycleEvent(ctx, "active session", delay, lifecycle, suspendHandler, guestAgentSocketPath, watchers)
}

func (m *manager) waitForSession(ctx context.Context, session *executor.Process, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	return m.waitForProcess(ctx, "active session", session, 0, lifecycle, suspendHandler, guestAgentSocketPath, watchers)
}

func (m *manager) waitForVM(ctx context.Context, qemu *executor.Process, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	return m.waitForProcess(ctx, "vm session", qemu, 0, lifecycle, suspendHandler, guestAgentSocketPath, watchers)
}

func (m *manager) waitForProcess(ctx context.Context, stage string, process *executor.Process, delay time.Duration, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	return launch.WaitForProcess(ctx, launch.ProcessWait{
		Stage:     stage,
		Process:   process,
		Delay:     delay,
		Lifecycle: lifecycle,
		PollDelay: defaultSocketPollInterval,
		Suspend: func(ctx context.Context) error {
			return handleSuspendRequest(ctx, lifecycle.Suspend(), suspendHandler)
		},
		Info: func(ctx context.Context) {
			m.printGuestInfo(ctx, guestAgentSocketPath, watchers)
		},
		Check: func(stage string) error {
			return firstUnexpectedExit(stage, watchers)
		},
		Cancel: func(stage string, err error) error {
			return &stageError{Stage: stage, Err: err}
		},
		ProcessError: func(stage string, name string, err error) error {
			return wrapCommandError(stage, name, err)
		},
	})
}

func (m *manager) waitForLifecycleEvent(ctx context.Context, stage string, delay time.Duration, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	return m.waitForProcess(ctx, stage, nil, delay, lifecycle, suspendHandler, guestAgentSocketPath, watchers)
}

func (m *manager) saveSuspendStateConnected(ctx context.Context, manifest *manifest.Manifest, qmpSocketPath string, client qmpClient, cid int, notifier notificationSink) error {
	statePath := vmStatePath(manifest)
	if err := launch.EnsureParentDirectories([]string{statePath}); err != nil {
		return &stageError{Stage: "qmp suspend", Err: err}
	}
	if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &stageError{Stage: "qmp suspend", Err: fmt.Errorf("remove stale vm state %q: %w", statePath, err)}
	}
	if err := qmpclient.SaveToFile(ctx, client, statePath, qmpclient.SaveWait{
		MigrationTimeout: m.effectiveQMPMigrationTimeout(),
		CommandTimeout:   m.effectiveQMPCommandTimeout(),
		PollDelay:        defaultMigrationPollDelay,
	}); err != nil {
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
