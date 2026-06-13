package launch

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type Starter struct {
	Host    Host
	Runtime Runtime
}

var ErrSavedSuspendExit = errors.New("saved suspend requested")

func IsSavedSuspendExit(err error) bool {
	return errors.Is(err, ErrSavedSuspendExit)
}

type Host interface {
	// NewLifecycle creates the launch lifecycle tied to context cancellation.
	NewLifecycle(context.CancelFunc) *Lifecycle
	// AcquireRuntimeLock acquires the manifest runtime lock for this launch.
	AcquireRuntimeLock(RuntimeLockSpec) (*RuntimeLock, error)
	// AcquireCID chooses the vsock CID for this launch.
	AcquireCID(*manifest.Manifest, *SuspendState) (int, error)
	// BuildQEMUCommand builds the qemu command for the finalized plan.
	BuildQEMUCommand(*manifest.Manifest, int, bool) (*exec.Cmd, error)
	// PrepareRuntimeState prepares host state required before process startup.
	PrepareRuntimeState(*Plan) error
	// RemoveSocketPaths removes stale runtime socket cleanup files.
	RemoveSocketPaths([]string) error
	// StartRuns starts configured host-side run processes.
	StartRuns(int, *manifest.Manifest) (executor.Group, error)
	// StartQEMU starts the qemu process.
	StartQEMU(*exec.Cmd) (*executor.Process, error)
	// InstallQMPShutdown installs the qmp-backed qemu shutdown hook.
	InstallQMPShutdown(*executor.Process, qmpclient.Client)
	// WaitForSockets waits for launch sockets to become available.
	WaitForSockets(context.Context, string, []string, executor.Group) error
	// WaitForQMP waits for and connects to the qmp monitor.
	WaitForQMP(context.Context, string, executor.Group) (qmpclient.Client, error)
	// RestoreRuntime restores saved vm state into the qmp connection.
	RestoreRuntime(context.Context, *Plan, qmpclient.Client) error
	// WriteGuestFiles writes configured files into the running guest.
	WriteGuestFiles(context.Context, *Plan, *Stats, executor.Group) error
	// WriteBackGuestFiles writes guest file changes back to the host.
	WriteBackGuestFiles(context.Context, *Plan, executor.Group) error
	// WaitForSSHReady waits for the guest ssh readiness signal.
	WaitForSSHReady(context.Context, string, executor.Group) error
	// ShutdownDelay returns the process shutdown delay for cleanup.
	ShutdownDelay() time.Duration
	// StatsOutput returns the writer used for launch stats output.
	StatsOutput() io.Writer
}

type Runtime interface {
	// New builds runtime/control handlers from started VM state.
	New(RuntimeSpec) (RuntimeResult, error)

	// SuspendHandler builds handler used for queued and signal-driven suspend requests.
	SuspendHandler(SuspendSpec) SuspendHandler

	// WaitForeground builds foreground wait function stored on runtime.
	WaitForeground(ForegroundSpec) func(context.Context, *Plan) error
}

type RuntimeSpec struct {
	Manifest        *manifest.Manifest
	Plan            *Plan
	Paths           RuntimePaths
	CID             int
	Stats           *Stats
	QMP             qmpclient.Client
	SuspendRequests *SuspendCoordinator
	Processes       *ProcessSet
	WaitForeground  func(context.Context, *Plan) error
	WriteBack       func(context.Context) error
	Cleanup         func() error
	CloseStats      func()
}

type RuntimeResult struct {
	Runtime        StartedRuntime
	ControlOptions []control.RouterOption
}

type StartedRuntime interface {
	SetReady()
	MarkSavedSuspend()
	SetWatchers(executor.Group)
	StartControl(context.Context, ...control.RouterOption) (*control.Server, error)
	Wait(context.Context, WaitMode) error
	Close() error
	QMP() qmpclient.Client
}

type SuspendSpec struct {
	Manifest        *manifest.Manifest
	Plan            *Plan
	QMP             qmpclient.Client
	CID             int
	WriteBackOnExit func() bool
}

type SuspendHandler interface {
	Handle(context.Context, *SuspendCoordinator) error
}

type ForegroundSpec struct {
	Plan           *Plan
	Stats          *Stats
	Runtime        func() StartedRuntime
	QMP            qmpclient.Client
	Lifecycle      *Lifecycle
	SuspendHandler SuspendHandler
	Processes      *ProcessSet
}

func (s Starter) Start(ctx context.Context, plan *Plan) (started StartedRuntime, err error) {
	if plan == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch plan is required")}
	}
	if s.Host == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch host is required")}
	}
	if s.Runtime == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch runtime is required")}
	}

	stats := NewStats(time.Now())
	launchCtx, cancelLaunch := context.WithCancel(ctx)
	lifecycle := s.Host.NewLifecycle(cancelLaunch)
	if lifecycle == nil {
		cancelLaunch()
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch host returned nil lifecycle")}
	}
	runtimeLock, err := s.Host.AcquireRuntimeLock(RuntimeLockSpec{
		Manifest:    plan.Manifest,
		ResumeState: plan.ResumeState,
		Lifecycle:   lifecycle,
		Cancel:      cancelLaunch,
		PID:         os.Getpid(),
	})
	if err != nil {
		return nil, &StageError{Stage: "preflight", Err: err}
	}
	if runtimeLock == nil {
		stopRuntimeLockLifecycle(lifecycle, cancelLaunch)
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch host returned nil runtime lock")}
	}

	processes := NewProcessSet()
	var qmp qmpclient.Client
	writeBackOnExit := false
	socketCleanupReached := false
	cleanupRuntime := func() error { return runtimeLock.Cleanup() }
	defer func() {
		if err == nil {
			return
		}
		if started != nil {
			if IsSavedSuspendExit(err) {
				started.MarkSavedSuspend()
			}
			err = errors.Join(err, started.Close())
			return
		}

		var cleanupErr error
		cleanupErr = errors.Join(cleanupErr, processes.Close(s.Host.ShutdownDelay()))
		cleanupErr = errors.Join(cleanupErr, cleanupRuntime())
		if qmp != nil {
			cleanupErr = errors.Join(cleanupErr, qmp.Disconnect())
		}
		if socketCleanupReached {
			cleanupErr = errors.Join(cleanupErr, s.Host.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles()))
		}
		finalizeStats(stats, s.Host.StatsOutput())()
		err = errors.Join(err, cleanupErr)
	}()

	cid, err := s.Host.AcquireCID(plan.Manifest, plan.ResumeState)
	if err != nil {
		return nil, &StageError{Stage: "preflight", Err: err}
	}
	qemuCmd, err := s.Host.BuildQEMUCommand(plan.Manifest, cid, plan.ResumeState != nil)
	if err != nil {
		return nil, &StageError{Stage: "preflight", Err: err}
	}
	if qemuCmd == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch host returned nil qemu command")}
	}
	plan.CID = cid
	plan.QEMUCommand = qemuCmd
	if err := s.Host.PrepareRuntimeState(plan); err != nil {
		return nil, &StageError{Stage: "preflight", Err: err}
	}
	socketCleanupReached = true

	runProcesses, err := s.Host.StartRuns(plan.CID, plan.Manifest)
	if err != nil {
		return nil, err
	}
	processes.AddGroup(runProcesses)
	if len(plan.VirtioFSSocketPaths) > 0 {
		if err := s.Host.WaitForSockets(launchCtx, "virtiofs startup", plan.VirtioFSSocketPaths, processes.Watchers()); err != nil {
			return nil, err
		}
	}

	stats.Timer(TimerBootStarted, time.Now())
	qemu, err := s.Host.StartQEMU(plan.QEMUCommand)
	if err != nil {
		return nil, WrapFixedStage("vm startup")(err)
	}
	if qemu == nil {
		return nil, WrapFixedStage("vm startup")(errors.New("launch host returned nil qemu process"))
	}
	processes.SetQEMU(qemu)
	qmp, err = s.Host.WaitForQMP(launchCtx, plan.Paths.QMPSocket, processes.Watchers())
	if err != nil {
		return nil, err
	}
	if qmp == nil {
		return nil, WrapFixedStage("vm startup")(errors.New("launch host returned nil qmp client"))
	}
	qmp = qmpclient.Serialized(qmp)
	stats.Timer(TimerQMPReady, time.Now())
	s.Host.InstallQMPShutdown(qemu, qmp)

	if plan.ResumeState != nil {
		if err := s.Host.RestoreRuntime(launchCtx, plan, qmp); err != nil {
			return nil, err
		}
		writeBackOnExit = true
	}

	suspendHandler := s.Runtime.SuspendHandler(SuspendSpec{
		Manifest: plan.Manifest,
		Plan:     plan,
		QMP:      qmp,
		CID:      plan.CID,
		WriteBackOnExit: func() bool {
			return writeBackOnExit
		},
	})
	if suspendHandler == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch runtime returned nil suspend handler")}
	}
	waitForeground := s.Runtime.WaitForeground(ForegroundSpec{
		Plan:           plan,
		Stats:          stats,
		Runtime:        func() StartedRuntime { return started },
		QMP:            qmp,
		Lifecycle:      lifecycle,
		SuspendHandler: suspendHandler,
		Processes:      processes,
	})
	if waitForeground == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch runtime returned nil foreground wait")}
	}
	result, err := s.Runtime.New(RuntimeSpec{
		Manifest:        plan.Manifest,
		Plan:            plan,
		Paths:           plan.Paths,
		CID:             plan.CID,
		Stats:           stats,
		QMP:             qmp,
		SuspendRequests: lifecycle.Suspend(),
		Processes:       processes,
		WaitForeground:  waitForeground,
		WriteBack: func(ctx context.Context) error {
			if !writeBackOnExit {
				return nil
			}
			return s.Host.WriteBackGuestFiles(ctx, plan, executor.Group{})
		},
		Cleanup: func() error {
			return errors.Join(s.Host.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles()), cleanupRuntime())
		},
		CloseStats: finalizeStats(stats, s.Host.StatsOutput()),
	})
	if err != nil {
		return nil, err
	}
	started = result.Runtime
	if started == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch runtime returned nil runtime")}
	}
	started.SetReady()
	if _, err := started.StartControl(launchCtx, result.ControlOptions...); err != nil {
		return nil, WrapFixedStage("control startup")(err)
	}
	if err := HandleQueuedSuspend(launchCtx, lifecycle, suspendHandler.Handle); err != nil {
		return nil, err
	}
	if plan.ResumeState == nil {
		if err := s.Host.WriteGuestFiles(launchCtx, plan, stats, processes.Watchers()); err != nil {
			return nil, err
		}
		stats.Timer(TimerFilesReady, time.Now())
		if plan.Paths.SSHReadySocket != "" {
			if err := s.Host.WaitForSSHReady(launchCtx, plan.Paths.SSHReadySocket, processes.Watchers()); err != nil {
				return nil, err
			}
		}
		stats.Timer(TimerSSHReady, time.Now())
		writeBackOnExit = true
	}
	return started, nil
}
