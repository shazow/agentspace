package manager

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type Runtime struct {
	manifest         *manifest.Manifest
	plan             *launch.Plan
	paths            launch.RuntimePaths
	cid              int
	stats            *runtimepkg.Stats
	qmp              qmpclient.Client
	suspendRequests  *launch.SuspendCoordinator
	waitForeground   func(context.Context, *launch.Plan) error
	collectInfo      func(context.Context, string, executor.Group) (runtimepkg.GuestInfo, error)
	processes        *runtimepkg.ProcessSet
	shutdownDelay    time.Duration
	qmpTimeout       time.Duration
	logger           *slog.Logger
	savedSuspendExit func(error) bool
	closeHooks       runtimepkg.CloseHooks
	savedSuspend     *runtimepkg.SavedSuspendState
	watchers         executor.Group
	hotplugStart     runtimepkg.HotplugStarter
	hotplugSockets   runtimepkg.HotplugSocketWaiter
	hotplugGuest     runtimepkg.HotplugGuest

	state   *runtimepkg.State
	closer  *runtimepkg.Closer
	control *runtimepkg.ControlServer
}

func newRuntime(config runtimepkg.RuntimeConfig) *Runtime {
	deps := config.Dependencies
	state := runtimepkg.NewState(RuntimeStarting)
	return &Runtime{
		manifest:         config.Manifest,
		paths:            config.Paths,
		cid:              config.CID,
		stats:            config.Stats,
		qmp:              qmpclient.Serialized(config.QMP),
		suspendRequests:  config.SuspendRequests,
		qmpTimeout:       deps.QMPTimeout,
		logger:           deps.Logger,
		savedSuspendExit: deps.SavedSuspendExit,
		collectInfo:      deps.CollectInfo,
		hotplugStart:     deps.HotplugStart,
		hotplugSockets:   deps.HotplugSockets,
		hotplugGuest:     deps.HotplugGuest,
		savedSuspend:     runtimepkg.NewSavedSuspendState(),
		state:            state,
		closer:           runtimepkg.NewCloser(state),
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
			return r.isSavedSuspendExit(err)
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
			return r.isSavedSuspendExit(err)
		},
	})
}

func (r *Runtime) Balloon(ctx context.Context, req controlpkg.BalloonRequest) (controlpkg.BalloonResponse, error) {
	return runtimepkg.ControlBalloon(ctx, r.manifest.QEMU.Devices.Balloon, r.qmp, r.qmpTimeout, req)
}

func (r *Runtime) isSavedSuspendExit(err error) bool {
	return r.savedSuspendExit != nil && r.savedSuspendExit(err)
}
