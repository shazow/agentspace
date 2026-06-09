package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type Runtime struct {
	manifest         *manifest.Manifest
	plan             *launch.Plan
	paths            launch.RuntimePaths
	cid              int
	stats            *Stats
	qmp              qmpclient.Client
	suspendRequests  *launch.SuspendCoordinator
	waitForeground   func(context.Context, *launch.Plan) error
	collectInfo      func(context.Context, string, executor.Group) (GuestInfo, error)
	processes        *ProcessSet
	shutdownDelay    time.Duration
	qmpTimeout       time.Duration
	logger           *slog.Logger
	savedSuspendExit func(error) bool
	closeHooks       CloseHooks
	savedSuspend     *SavedSuspendState
	watchers         executor.Group
	hotplugStart     HotplugStarter
	hotplugSockets   HotplugSocketWaiter
	hotplugGuest     HotplugGuest

	state   *State
	closer  *Closer
	control *ControlServer
}

func New(config RuntimeConfig) *Runtime {
	deps := config.Dependencies
	state := NewState(control.RuntimeStarting)
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
		savedSuspend:     NewSavedSuspendState(),
		state:            state,
		closer:           NewCloser(state),
	}
}

func (r *Runtime) SetReady() {
	MarkReady(r.state)
}

func (r *Runtime) MarkSavedSuspend() {
	r.savedSuspend.MarkSaved()
}

func (r *Runtime) SetWatchers(watchers executor.Group) {
	r.watchers = watchers
}

func (r *Runtime) SetProcesses(processes *ProcessSet, shutdownDelay time.Duration) {
	r.processes = processes
	r.shutdownDelay = shutdownDelay
}

func (r *Runtime) SetForegroundWait(plan *launch.Plan, waitForeground func(context.Context, *launch.Plan) error) {
	r.plan = plan
	r.waitForeground = waitForeground
}

func (r *Runtime) SetCloseHooks(hooks CloseHooks) {
	r.closeHooks = hooks
}

func (r *Runtime) QMP() qmpclient.Client {
	return r.qmp
}

func (r *Runtime) StartControl(ctx context.Context) error {
	controlServer, err := StartControl(ctx, r.paths.ControlSocket, r, r.logger)
	r.control = controlServer
	return err
}

func (r *Runtime) Close() error {
	return r.closer.Close(CloseActions{
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
		return ControlWaitForeground(ctx, ForegroundWaitOperation{})
	}
	plan := launch.PlanForWaitMode(r.plan, mode)
	return ControlWaitForeground(ctx, ForegroundWaitOperation{
		SavedSuspend: r.savedSuspend,
		Wait: func(ctx context.Context) error {
			return r.waitForeground(ctx, plan)
		},
		SavedSuspendExit: func(err error) bool {
			return r.isSavedSuspendExit(err)
		},
	})
}

func (r *Runtime) Status(ctx context.Context, req control.StatusRequest) (control.StatusResponse, error) {
	_ = ctx
	return Status(r.state, r.cid, control.StatusPaths{
		ControlSocket:    r.paths.ControlSocket,
		QMPSocket:        r.paths.QMPSocket,
		GuestAgentSocket: r.paths.GuestAgentSocket,
		SSHReadySocket:   r.paths.SSHReadySocket,
	}, r.stats), nil
}

func (r *Runtime) Info(ctx context.Context, req control.InfoRequest) (control.InfoResponse, error) {
	return ControlInfo(ctx, runtimeInfoCollector{runtime: r})
}

type runtimeInfoCollector struct {
	runtime *Runtime
}

func (c runtimeInfoCollector) CollectInfo(ctx context.Context) (GuestInfo, error) {
	if c.runtime.collectInfo == nil {
		return GuestInfo{}, fmt.Errorf("runtime info collector is not configured")
	}
	return c.runtime.collectInfo(ctx, c.runtime.paths.GuestAgentSocket, c.runtime.watchers)
}

func (r *Runtime) Suspend(ctx context.Context, req control.SuspendRequest) (control.SuspendResponse, error) {
	return ControlSuspend(ctx, SuspendOperation{
		State:       r.state,
		Requester:   r.suspendRequests,
		VMStatePath: launch.VMStatePath(r.manifest),
		SavedSuspendExit: func(err error) bool {
			return r.isSavedSuspendExit(err)
		},
	})
}

func (r *Runtime) Balloon(ctx context.Context, req control.BalloonRequest) (control.BalloonResponse, error) {
	return ControlBalloon(ctx, r.manifest.QEMU.Devices.Balloon, r.qmp, r.qmpTimeout, req)
}

func (r *Runtime) isSavedSuspendExit(err error) bool {
	return r.savedSuspendExit != nil && r.savedSuspendExit(err)
}
