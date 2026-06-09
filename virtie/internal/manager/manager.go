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
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qga"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

const (
	defaultSSHRetryDelay      = 500 * time.Millisecond
	defaultShutdownDelay      = 15 * time.Second
	defaultMigrationPollDelay = 100 * time.Millisecond
	sshRetryOutputRevealDelay = 250 * time.Millisecond
)

var errSavedSuspendExit = errors.New("saved suspend requested")

func isSavedSuspendExit(err error) bool {
	return errors.Is(err, errSavedSuspendExit)
}

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
type Config = launch.Config
type Runtime = runtimepkg.Runtime

type Launcher struct {
	manager *manager
}

func DefaultConfig() Config {
	return Config{
		Locker:              &fileLocker{},
		VSockCIDChecker:     newHostVSockCIDChecker(),
		Runner:              &executor.Runner{},
		SocketWaiter:        &pollingSocketWaiter{},
		QMPDialer:           &qmpclient.SocketMonitorDialer{},
		GuestAgentDialer:    &qga.SocketDialer{},
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
	locker              launch.Locker
	vsockCIDChecker     launch.VSockCIDChecker
	runner              launch.Runner
	socketWaiter        launch.SocketWaiter
	qmpDialer           qmpclient.Dialer
	guestAgentDialer    qga.Dialer
	sshReadyDialer      launch.SSHReadyDialer
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
	pidSignaler         launch.PIDSignaler
	notifier            launch.NotificationSink
}

func newManager() *manager {
	return newManagerFromConfig(DefaultConfig())
}

func newManagerFromConfig(config launch.Config) *manager {
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
	return l.launchWithOptions(ctx, manifest, remoteCommand, launch.Options{Resume: launch.ResumeModeNo, SSH: true})
}

func (l *Launcher) launchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options launch.Options) error {
	plan, err := l.Plan(ctx, launch.Spec{Manifest: manifest, RemoteCommand: remoteCommand, Options: options})
	if err != nil {
		return err
	}
	return l.manager.launchWithPlan(ctx, plan)
}

func (m *manager) launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) error {
	return m.launchWithOptions(ctx, manifest, remoteCommand, launch.Options{Resume: launch.ResumeModeNo, SSH: true})
}

func (m *manager) launchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options launch.Options) error {
	plan, err := m.planLaunch(launch.Spec{Manifest: manifest, RemoteCommand: remoteCommand, Options: options})
	if err != nil {
		return err
	}
	return m.launchWithPlan(ctx, plan)
}

func (m *manager) planLaunch(spec launch.Spec) (*launch.Plan, error) {
	cfg := spec.Manifest
	options := spec.Options
	resumeMode, err := launch.NormalizeResumeMode(options.Resume)
	if err != nil {
		return nil, &launch.StageError{Stage: "preflight", Err: err}
	}
	resumeState, err := launch.ResolveResumeState(cfg, resumeMode)
	if err != nil {
		return nil, &launch.StageError{Stage: "restore", Err: err}
	}
	notifier := launch.SelectNotifier(cfg, m.notifier, func(cfg *manifest.Manifest) launch.NotificationSink {
		return newCommandNotifier(cfg, m.logger)
	})
	plan, err := launch.BuildPlan(spec, resumeState, notifier)
	if err != nil {
		return nil, &launch.StageError{Stage: "preflight", Err: err}
	}
	return plan, nil
}

func (m *manager) launchWithPlan(ctx context.Context, plan *launch.Plan) (err error) {
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

func (m *manager) startWithPlan(ctx context.Context, plan *launch.Plan) (runtime *Runtime, err error) {
	stats := runtimepkg.NewStats(time.Now())
	manifest := plan.Manifest

	launchCtx, cancelLaunch := context.WithCancel(ctx)
	lifecycle := launch.NewSignalLifecycle(m.signals, cancelLaunch)

	runtimeLock, err := launch.AcquireRuntimeLock(launch.RuntimeLockSpec{
		Manifest:    manifest,
		ResumeState: plan.ResumeState,
		Locker:      m.locker,
		Lifecycle:   lifecycle,
		Cancel:      cancelLaunch,
		PID:         os.Getpid(),
	})
	if err != nil {
		return nil, &launch.StageError{Stage: "preflight", Err: err}
	}

	cleanupRuntime := func() error {
		return runtimeLock.Cleanup()
	}

	if err := launch.SetupLockedPlan(launch.LockedPlanSetup{
		Plan:      plan,
		Checker:   m.vsockCIDChecker,
		BuildQEMU: buildQEMUCommand,
		Logger:    m.logger,
		Cleanup:   cleanupRuntime,
	}); err != nil {
		return nil, &launch.StageError{Stage: "preflight", Err: err}
	}

	processes := runtimepkg.NewProcessSet()
	var qmpClient qmpclient.Client
	writeBackOnExit := runtimepkg.NewWriteBackState()
	defer func() {
		if err != nil {
			var startedRuntime runtimepkg.StartedRuntime
			if runtime != nil {
				startedRuntime = runtime
			}
			err = runtimepkg.CleanupConfiguredStartError(
				err,
				startedRuntime,
				runtimepkg.StartupFailureConfig{
					Processes:     processes,
					ShutdownDelay: m.shutdownDelay,
					LockCleanup:   cleanupRuntime,
					QMP:           qmpClient,
					SocketCleanup: []func() error{
						func() error {
							return launch.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles())
						},
					},
					Stats:       stats,
					StatsOutput: m.outputWriter(),
				},
				isSavedSuspendExit,
			)
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
		writeBackOnExit.Enable()
	}
	suspendHandler := newLaunchSuspendHandler(m, plan.Manifest, plan.Paths.QMPSocket, qmpClient, plan.CID, plan.Notifier, func() bool {
		return writeBackOnExit.Enabled()
	})
	if err := launch.ActivateRuntime(launchCtx, launch.RuntimeActivation{
		Lifecycle: lifecycle,
		MarkReady: runtime.SetReady,
		Configure: func() {
			runtime.SetForegroundWait(plan, func(ctx context.Context, waitPlan *launch.Plan) error {
				return m.waitForLaunchForeground(ctx, waitPlan, stats, runtime, qmpClient, lifecycle, suspendHandler, processes)
			})
			runtime.SetCloseHooks(runtimepkg.ConfiguredCloseHooks(runtimepkg.CloseHookConfig{
				WriteBackState: writeBackOnExit,
				WriteBack: func(ctx context.Context) error {
					return m.writeBackGuestFiles(ctx, plan.Manifest, executor.Group{})
				},
				Cleanup: []func() error{
					func() error {
						return launch.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles())
					},
					cleanupRuntime,
				},
				Stats:       stats,
				StatsOutput: m.outputWriter(),
			}))
		},
		StartControl: runtime.StartControl,
		WrapControl:  launch.WrapFixedStage("control startup"),
		HandleSuspend: func(ctx context.Context, coordinator *launch.SuspendCoordinator) error {
			return handleSuspendRequest(ctx, coordinator, suspendHandler)
		},
		Provision: launch.GuestProvision{
			Plan:  plan,
			Stats: stats,
			WriteFiles: func(ctx context.Context) error {
				return m.writeGuestFiles(ctx, plan.Manifest, stats, processes.Watchers())
			},
			WaitSSHReady: func(ctx context.Context, socketPath string) error {
				m.logger.Info("waiting for ssh readiness")
				return m.waitForSSHReady(ctx, socketPath, processes.Watchers())
			},
		},
		EnableWriteBack: writeBackOnExit.Enable,
	}); err != nil {
		return nil, err
	}

	return runtime, nil
}

func (m *manager) startLaunchRuntime(ctx context.Context, plan *launch.Plan, stats *runtimepkg.Stats, lifecycle *launch.Lifecycle, processes *runtimepkg.ProcessSet) (*Runtime, qmpclient.Client, error) {
	started, err := launch.StartRuntimeProcesses(ctx, launch.RuntimeStartup{
		Plan:           plan,
		Processes:      processes,
		Stats:          stats,
		Runner:         m.runner,
		Logger:         m.logger,
		StartRuns:      m.startRuns,
		WaitForSockets: m.waitForSockets,
		WaitForQMP:     m.waitForQMP,
		WrapVMStartup:  launch.WrapFixedStage("vm startup"),
	})
	if err != nil {
		return nil, nil, err
	}
	runtimeDeps := runtimepkg.Dependencies{
		QMPTimeout:       m.effectiveQMPCommandTimeout(),
		Logger:           m.logger,
		SavedSuspendExit: isSavedSuspendExit,
		CollectInfo: func(ctx context.Context, socketPath string, watchers executor.Group) (runtimepkg.GuestInfo, error) {
			info, err := m.collectGuestInfo(ctx, socketPath, watchers)
			if err != nil {
				return runtimepkg.GuestInfo{}, err
			}
			return runtimepkg.GuestInfo{ProcessList: info.ProcessList}, nil
		},
	}
	configureRuntimeHotplugDependencies(&runtimeDeps, m, plan.Manifest)
	runtime := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest:        plan.Manifest,
		Paths:           plan.Paths,
		CID:             plan.CID,
		Stats:           stats,
		QMP:             started.QMP,
		SuspendRequests: lifecycle.Suspend(),
		Dependencies:    runtimeDeps,
	})
	runtime.SetProcesses(processes, m.shutdownDelay)
	client := runtime.QMP()
	launch.FinalizeRuntimeStartup(launch.RuntimeStartupFinalize{
		QEMU:        started.QEMU,
		QMP:         client,
		Stats:       stats,
		QuitTimeout: m.effectiveQMPQuitTimeout(),
	})
	return runtime, client, nil
}

func (m *manager) restoreLaunchRuntime(ctx context.Context, plan *launch.Plan, client qmpclient.Client) error {
	return launch.RestoreRuntime(ctx, launch.RuntimeRestore{
		Plan:   plan,
		Logger: m.logger,
		Restore: func(ctx context.Context, vmStatePath string) error {
			return qmpclient.RestoreFromFile(ctx, client, vmStatePath, qmpclient.RestoreWait{
				MigrationTimeout: m.effectiveQMPMigrationTimeout(),
				CommandTimeout:   m.effectiveQMPCommandTimeout(),
				PollDelay:        defaultMigrationPollDelay,
			})
		},
		Wrap: launch.WrapFixedStage("restore"),
	})
}

func removeRestoredSuspendState(plan *launch.Plan) error {
	if err := launch.RemoveRestoredSuspendState(plan); err != nil {
		return &launch.StageError{Stage: "restore", Err: err}
	}
	return nil
}

func (m *manager) waitForLaunchForeground(
	ctx context.Context,
	plan *launch.Plan,
	stats *runtimepkg.Stats,
	runtime *Runtime,
	qmpClient qmpclient.Client,
	lifecycle *launch.Lifecycle,
	suspendHandler *launchSuspendHandler,
	processes *runtimepkg.ProcessSet,
) error {
	return launch.WaitForeground(ctx, launch.ForegroundWait{
		Plan:      plan,
		Runtime:   runtime,
		Processes: processes,
		Logger:    m.logger,
		Output:    m.outputWriter(),
		StartFeatures: func(ctx context.Context) {
			processes.SetFeatures(startOptionalFeatureTasks(ctx, optionalFeatureRuntime{
				qmpTimeout: m.effectiveQMPCommandTimeout(),
				notifier:   plan.Notifier,
			}, plan.Manifest, qmpClient))
		},
		RunSSH: func(ctx context.Context) error {
			return m.runSSHSession(ctx, plan, stats, lifecycle, suspendHandler, processes)
		},
		WaitVM: func(ctx context.Context, qemu *executor.Process, watchers executor.Group) error {
			return m.waitForVM(ctx, qemu, lifecycle, suspendHandler, plan.Paths.GuestAgentSocket, watchers)
		},
		RemoveRestored: removeRestoredSuspendState,
	})
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
		return executor.Group{}, &launch.StageError{Stage: "run startup", Err: err}
	}
	return runs, nil
}

func (m *manager) waitForSockets(ctx context.Context, stage string, socketPaths []string, watchers executor.Group) error {
	return m.waitForLaunchSockets(ctx, stage, socketPaths, watchers)
}

func (m *manager) waitForQMP(ctx context.Context, socketPath string, watchers executor.Group) (qmpclient.Client, error) {
	dialer := m.qmpDialer
	if dialer == nil {
		dialer = &qmpclient.SocketMonitorDialer{}
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
		Watchers:       watchers,
	})
}

func (m *manager) waitForLaunchSockets(ctx context.Context, stage string, socketPaths []string, watchers executor.Group) error {
	return launch.WaitForSockets(ctx, launch.SocketWait{
		Stage:        stage,
		SocketPaths:  socketPaths,
		SocketWaiter: m.socketWaiter,
		PollDelay:    defaultSocketPollInterval,
		Watchers:     watchers,
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
	client        qmpclient.Client
	cid           int
	notifier      launch.NotificationSink
	writeBack     func() bool
	once          sync.Once
	err           error
}

func newLaunchSuspendHandler(manager *manager, manifest *manifest.Manifest, qmpSocketPath string, client qmpclient.Client, cid int, notifier launch.NotificationSink, writeBack func() bool) *launchSuspendHandler {
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

func handleSuspendRequest(ctx context.Context, coordinator *launch.SuspendCoordinator, handler *launchSuspendHandler) error {
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
	plan *launch.Plan,
	stats *runtimepkg.Stats,
	lifecycle *launch.Lifecycle,
	suspendHandler *launchSuspendHandler,
	processes *runtimepkg.ProcessSet,
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
		EnsureKey:  m.ensureSSHAutoprovisionKey,
		InstallKey: m.installSSHAutoprovisionKey,
	})
}

func (m *manager) waitBeforeSSHRetry(ctx context.Context, launchManifest *manifest.Manifest, lifecycle *launch.Lifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	delay := launchManifest.SSHRetryDelay(m.sshRetryDelay)
	if delay <= 0 {
		delay = m.sshRetryDelay
	}
	if delay <= 0 {
		return nil
	}

	return m.waitForLifecycleEvent(ctx, "active session", delay, lifecycle, suspendHandler, guestAgentSocketPath, watchers)
}

func (m *manager) waitForSession(ctx context.Context, session *executor.Process, lifecycle *launch.Lifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	return m.waitForProcess(ctx, "active session", session, 0, lifecycle, suspendHandler, guestAgentSocketPath, watchers)
}

func (m *manager) waitForVM(ctx context.Context, qemu *executor.Process, lifecycle *launch.Lifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	return m.waitForProcess(ctx, "vm session", qemu, 0, lifecycle, suspendHandler, guestAgentSocketPath, watchers)
}

func (m *manager) waitForProcess(ctx context.Context, stage string, process *executor.Process, delay time.Duration, lifecycle *launch.Lifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	return launch.WaitForLifecycleProcess(ctx, launch.LifecycleProcessWait{
		Stage:     stage,
		Process:   process,
		Delay:     delay,
		Lifecycle: lifecycle,
		Watchers:  watchers,
		PollDelay: defaultSocketPollInterval,
		Suspend: func(ctx context.Context) error {
			return handleSuspendRequest(ctx, lifecycle.Suspend(), suspendHandler)
		},
		Info: func(ctx context.Context) {
			m.printGuestInfo(ctx, guestAgentSocketPath, watchers)
		},
	})
}

func (m *manager) waitForLifecycleEvent(ctx context.Context, stage string, delay time.Duration, lifecycle *launch.Lifecycle, suspendHandler *launchSuspendHandler, guestAgentSocketPath string, watchers executor.Group) error {
	return m.waitForProcess(ctx, stage, nil, delay, lifecycle, suspendHandler, guestAgentSocketPath, watchers)
}

func (m *manager) saveSuspendStateConnected(ctx context.Context, manifest *manifest.Manifest, qmpSocketPath string, client qmpclient.Client, cid int, notifier launch.NotificationSink) error {
	return launch.SaveRuntimeSuspend(ctx, launch.RuntimeSuspendSave{
		Manifest:      manifest,
		QMPSocketPath: qmpSocketPath,
		CID:           cid,
		Notifier:      notifier,
		Save: func(ctx context.Context, vmStatePath string) error {
			return qmpclient.SaveToFile(ctx, client, vmStatePath, qmpclient.SaveWait{
				MigrationTimeout: m.effectiveQMPMigrationTimeout(),
				CommandTimeout:   m.effectiveQMPCommandTimeout(),
				PollDelay:        defaultMigrationPollDelay,
			})
		},
		Wrap: launch.WrapFixedStage("qmp suspend"),
	})
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
