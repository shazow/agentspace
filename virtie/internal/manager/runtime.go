package manager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"syscall"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type Runtime struct {
	manifest        *manifest.Manifest
	plan            *launch.Plan
	paths           launch.RuntimePaths
	cid             int
	stats           *runtimepkg.Stats
	qmp             qmpclient.Client
	suspendRequests *launch.SuspendCoordinator
	waitForeground  func(context.Context, *launch.Plan) error
	collectInfo     func(context.Context, string, executor.Group) (runtimepkg.GuestInfo, error)
	processes       *runtimepkg.ProcessSet
	shutdownDelay   time.Duration
	qmpTimeout      time.Duration
	logger          *slog.Logger
	closeHooks      runtimepkg.CloseHooks
	savedSuspend    *runtimepkg.SavedSuspendState
	watchers        executor.Group
	hotplugStart    runtimeHotplugStarter
	hotplugSockets  runtimeHotplugSocketWaiter
	hotplugGuest    runtimeHotplugGuest

	state   *runtimepkg.State
	closer  *runtimepkg.Closer
	control *runtimepkg.ControlServer
}

type runtimeDependencies struct {
	QMPTimeout     time.Duration
	Logger         *slog.Logger
	CollectInfo    func(context.Context, string, executor.Group) (runtimepkg.GuestInfo, error)
	HotplugStart   runtimeHotplugStarter
	HotplugSockets runtimeHotplugSocketWaiter
	HotplugGuest   runtimeHotplugGuest
}

type runtimeHotplugStarter interface {
	Start(context.Context, *exec.Cmd) (*executor.Process, error)
	Stop(*executor.Process) error
	SignalPIDGroup(int, syscall.Signal) error
}

type runtimeHotplugSocketWaiter interface {
	Wait(context.Context, string, []string, *executor.Process) error
}

type runtimeHotplugGuest interface {
	Run(context.Context, []string) error
}

func newRuntime(manifest *manifest.Manifest, paths launch.RuntimePaths, cid int, stats *runtimepkg.Stats, qmp qmpclient.Client, suspendRequests *launch.SuspendCoordinator, deps runtimeDependencies) *Runtime {
	state := runtimepkg.NewState(RuntimeStarting)
	return &Runtime{
		manifest:        manifest,
		paths:           paths,
		cid:             cid,
		stats:           stats,
		qmp:             qmpclient.Serialized(qmp),
		suspendRequests: suspendRequests,
		qmpTimeout:      deps.QMPTimeout,
		logger:          deps.Logger,
		collectInfo:     deps.CollectInfo,
		hotplugStart:    deps.HotplugStart,
		hotplugSockets:  deps.HotplugSockets,
		hotplugGuest:    deps.HotplugGuest,
		savedSuspend:    runtimepkg.NewSavedSuspendState(),
		state:           state,
		closer:          runtimepkg.NewCloser(state),
	}
}

func (r *Runtime) SetReady() {
	runtimepkg.MarkReady(r.state)
}

func (r *Runtime) MarkSavedSuspend() {
	r.savedSuspend.MarkSaved()
}

func (r *Runtime) SetWatchers(watchers executor.Group) {
	r.watchers = watchers
}

func (r *Runtime) SetProcesses(processes *runtimepkg.ProcessSet, shutdownDelay time.Duration) {
	r.processes = processes
	r.shutdownDelay = shutdownDelay
}

func (r *Runtime) SetForegroundWait(plan *launch.Plan, waitForeground func(context.Context, *launch.Plan) error) {
	r.plan = plan
	r.waitForeground = waitForeground
}

func (r *Runtime) SetCloseHooks(hooks runtimepkg.CloseHooks) {
	r.closeHooks = hooks
}

func (r *Runtime) QMP() qmpclient.Client {
	return r.qmp
}

func (r *Runtime) StartControl(ctx context.Context) error {
	controlServer, err := runtimepkg.StartControl(ctx, r.paths.ControlSocket, r, r.logger)
	r.control = controlServer
	return err
}

func (r *Runtime) Close() error {
	return r.closer.Close(runtimepkg.CloseActions{
		WriteBack:        r.closeHooks.WriteBack,
		WriteBackTimeout: r.qmpTimeout,
		SkipWriteBack:    r.savedSuspend.Saved(),
		Control:          r.control,
		Processes:        r.processes,
		ShutdownDelay:    r.shutdownDelay,
		QMP:              r.qmp,
		Cleanup:          r.closeHooks.Cleanup,
		Stats:            r.closeHooks.Stats,
	})
}

func (r *Runtime) Wait(ctx context.Context, mode launch.WaitMode) error {
	if r.plan == nil || r.processes == nil || r.waitForeground == nil {
		return runtimepkg.ControlWaitForeground(ctx, runtimepkg.ForegroundWaitOperation{})
	}
	plan := launch.PlanForWaitMode(r.plan, mode)
	return runtimepkg.ControlWaitForeground(ctx, runtimepkg.ForegroundWaitOperation{
		SavedSuspend: r.savedSuspend,
		Wait: func(ctx context.Context) error {
			return r.waitForeground(ctx, plan)
		},
		SavedSuspendExit: func(err error) bool {
			return errors.Is(err, errSavedSuspendExit)
		},
	})
}

func (r *Runtime) Status(ctx context.Context, req controlpkg.StatusRequest) (controlpkg.StatusResponse, error) {
	_ = ctx
	return runtimepkg.Status(r.state, r.cid, controlpkg.StatusPaths{
		ControlSocket:    r.paths.ControlSocket,
		QMPSocket:        r.paths.QMPSocket,
		GuestAgentSocket: r.paths.GuestAgentSocket,
		SSHReadySocket:   r.paths.SSHReadySocket,
	}, r.stats), nil
}

func (r *Runtime) Info(ctx context.Context, req controlpkg.InfoRequest) (controlpkg.InfoResponse, error) {
	return runtimepkg.ControlInfo(ctx, runtimeInfoCollector{runtime: r})
}

type runtimeInfoCollector struct {
	runtime *Runtime
}

func (c runtimeInfoCollector) CollectInfo(ctx context.Context) (runtimepkg.GuestInfo, error) {
	if c.runtime.collectInfo == nil {
		return runtimepkg.GuestInfo{}, fmt.Errorf("runtime info collector is not configured")
	}
	return c.runtime.collectInfo(ctx, c.runtime.paths.GuestAgentSocket, c.runtime.watchers)
}

func (r *Runtime) Suspend(ctx context.Context, req controlpkg.SuspendRequest) (controlpkg.SuspendResponse, error) {
	return runtimepkg.ControlSuspend(ctx, runtimepkg.SuspendOperation{
		State:       r.state,
		Requester:   r.suspendRequests,
		VMStatePath: launch.VMStatePath(r.manifest),
		SavedSuspendExit: func(err error) bool {
			return errors.Is(err, errSavedSuspendExit)
		},
	})
}

func (r *Runtime) Balloon(ctx context.Context, req controlpkg.BalloonRequest) (controlpkg.BalloonResponse, error) {
	return runtimepkg.ControlBalloon(ctx, r.manifest.QEMU.Devices.Balloon, r.qmp, r.qmpTimeout, req)
}
