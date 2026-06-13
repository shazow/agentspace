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

type Core struct {
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

	state   *state
	closer  *closer
	control *control.Server
}

func New(config RuntimeConfig) *Core {
	deps := config.Dependencies
	state := newState(control.RuntimeStarting)
	return &Core{
		manifest:         config.Manifest,
		paths:            config.Paths,
		plan:             config.Plan,
		cid:              config.CID,
		stats:            config.Stats,
		qmp:              qmpclient.Serialized(config.QMP),
		suspendRequests:  config.SuspendRequests,
		waitForeground:   config.WaitForeground,
		qmpTimeout:       deps.QMPTimeout,
		logger:           deps.Logger,
		savedSuspendExit: deps.SavedSuspendExit,
		collectInfo:      deps.CollectInfo,
		processes:        config.Processes,
		shutdownDelay:    config.ShutdownDelay,
		closeHooks:       config.CloseHooks,
		state:            state,
		closer:           newCloser(state),
	}
}

func (r *Core) SetReady() {
	markReady(r.state)
}

func (r *Core) MarkSavedSuspend() {
	r.savedSuspend.Store(true)
}

func (r *Core) SetWatchers(watchers executor.Group) {
	r.watchers = watchers
}

func (r *Core) QMP() qmpclient.Client {
	return r.qmp
}

func (r *Core) StartControl(ctx context.Context, options ...control.RouterOption) (*control.Server, error) {
	routerOptions := []control.RouterOption{
		control.WithSuspend(r),
		control.WithBalloon(r),
	}
	routerOptions = append(routerOptions, options...)
	router, err := control.NewRouter(r, routerOptions...)
	if err != nil {
		return nil, err
	}
	controlServer, err := StartControl(ctx, r.paths.ControlSocket, router, r.logger)
	if err == nil {
		r.control = controlServer
	}
	return controlServer, err
}

func (r *Core) Close() error {
	return r.closer.Close(closeActions{
		shutdownResources: shutdownResources{
			Processes:     r.processes,
			ShutdownDelay: r.shutdownDelay,
			QMP:           r.qmp,
			Stats:         r.closeHooks.Stats,
		},
		WriteBack:        r.closeHooks.WriteBack,
		WriteBackTimeout: r.qmpTimeout,
		SkipWriteBack:    r.savedSuspend.Load(),
		Control:          r.control,
		Cleanup:          r.closeHooks.Cleanup,
	})
}

func (r *Core) Wait(ctx context.Context, mode launch.WaitMode) error {
	if r.plan == nil || r.processes == nil || r.waitForeground == nil {
		return control.FailedPrecondition(errForegroundWaitNotConfigured)
	}
	plan := launch.PlanForWaitMode(r.plan, mode)
	err := r.waitForeground(ctx, plan)
	if err != nil && r.isSavedSuspendExit(err) {
		r.MarkSavedSuspend()
	}
	return err
}

func (r *Core) Status(ctx context.Context, req control.StatusRequest) (control.StatusResponse, error) {
	_ = ctx
	return status(r.state, r.cid, control.StatusPaths{
		ControlSocket:    r.paths.ControlSocket,
		QMPSocket:        r.paths.QMPSocket,
		GuestAgentSocket: r.paths.GuestAgentSocket,
		SSHReadySocket:   r.paths.SSHReadySocket,
	}, r.stats), nil
}

func (r *Core) Info(ctx context.Context, req control.InfoRequest) (control.InfoResponse, error) {
	if r.collectInfo == nil {
		return control.InfoResponse{}, control.FailedPrecondition(errors.New("runtime info collector is not configured"))
	}
	info, err := r.collectInfo(ctx, r.paths.GuestAgentSocket, r.watchers)
	if err != nil {
		return control.InfoResponse{}, control.FailedPrecondition(err)
	}
	return control.InfoResponse{ProcessList: info.ProcessList}, nil
}

func (r *Core) Suspend(ctx context.Context, req control.SuspendRequest) (control.SuspendResponse, error) {
	if r.suspendRequests == nil {
		return control.SuspendResponse{}, control.FailedPrecondition(errSuspendNotReady)
	}
	err := queueSuspend(ctx, r.state, r.suspendRequests, r.isSavedSuspendExit)
	if errors.Is(err, errSuspendNotReady) {
		return control.SuspendResponse{}, control.FailedPrecondition(err)
	}
	if err != nil {
		return control.SuspendResponse{}, err
	}
	return control.SuspendResponse{Saved: true, VMStatePath: launch.VMStatePath(r.manifest)}, nil
}

func (r *Core) Balloon(ctx context.Context, req control.BalloonRequest) (control.BalloonResponse, error) {
	resp, err := balloon(ctx, r.manifest.QEMU.Devices.Balloon, r.qmp, r.qmpTimeout, req)
	if errors.Is(err, errBalloonNotConfigured) {
		return control.BalloonResponse{}, control.FailedPrecondition(err)
	}
	return resp, err
}

func (r *Core) isSavedSuspendExit(err error) bool {
	return r.savedSuspendExit != nil && r.savedSuspendExit(err)
}
