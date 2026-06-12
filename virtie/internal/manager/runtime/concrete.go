package runtime

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
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
	savedSuspend     atomic.Bool
	watchers         executor.Group
	hotplugStart     HotplugStarter
	hotplugSockets   HotplugSocketWaiter
	hotplugGuest     HotplugGuest

	state   *State
	closer  *Closer
	control *control.Server
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
		state:            state,
		closer:           NewCloser(state),
	}
}

func (r *Runtime) SetReady() {
	MarkReady(r.state)
}

func (r *Runtime) MarkSavedSuspend() {
	r.savedSuspend.Store(true)
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

func (r *Runtime) StartControl(ctx context.Context) (*control.Server, error) {
	controlServer, err := StartControl(ctx, r.paths.ControlSocket, r, r.logger)
	if err == nil {
		r.control = controlServer
	}
	return controlServer, err
}

func (r *Runtime) Close() error {
	return r.closer.Close(CloseActions{
		WriteBack:        r.closeHooks.WriteBack,
		WriteBackTimeout: r.qmpTimeout,
		SkipWriteBack:    r.savedSuspend.Load(),
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
		return control.FailedPrecondition(ErrForegroundWaitNotConfigured)
	}
	plan := launch.PlanForWaitMode(r.plan, mode)
	err := r.waitForeground(ctx, plan)
	if err != nil && r.isSavedSuspendExit(err) {
		r.MarkSavedSuspend()
	}
	return err
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
	if r.collectInfo == nil {
		return control.InfoResponse{}, control.FailedPrecondition(errors.New("runtime info collector is not configured"))
	}
	info, err := r.collectInfo(ctx, r.paths.GuestAgentSocket, r.watchers)
	if err != nil {
		return control.InfoResponse{}, control.FailedPrecondition(err)
	}
	return control.InfoResponse{ProcessList: info.ProcessList}, nil
}

func (r *Runtime) Suspend(ctx context.Context, req control.SuspendRequest) (control.SuspendResponse, error) {
	if r.suspendRequests == nil {
		return control.SuspendResponse{}, control.FailedPrecondition(ErrSuspendNotReady)
	}
	err := QueueSuspend(ctx, r.state, r.suspendRequests, r.isSavedSuspendExit)
	if errors.Is(err, ErrSuspendNotReady) {
		return control.SuspendResponse{}, control.FailedPrecondition(err)
	}
	if err != nil {
		return control.SuspendResponse{}, err
	}
	return control.SuspendResponse{Saved: true, VMStatePath: launch.VMStatePath(r.manifest)}, nil
}

func (r *Runtime) Balloon(ctx context.Context, req control.BalloonRequest) (control.BalloonResponse, error) {
	resp, err := Balloon(ctx, r.manifest.QEMU.Devices.Balloon, r.qmp, r.qmpTimeout, req)
	if errors.Is(err, ErrBalloonNotConfigured) {
		return control.BalloonResponse{}, control.FailedPrecondition(err)
	}
	return resp, err
}

func (r *Runtime) isSavedSuspendExit(err error) bool {
	return r.savedSuspendExit != nil && r.savedSuspendExit(err)
}
