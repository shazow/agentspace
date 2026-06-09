package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type Runtime struct {
	manager         *manager
	manifest        *manifest.Manifest
	plan            *Plan
	paths           RuntimePaths
	cid             int
	stats           *launchStats
	qmp             qmpClient
	suspendRequests *launchSuspendCoordinator
	lifecycle       *launchLifecycle
	suspendHandler  *launchSuspendHandler
	processes       *ProcessSet
	shutdownDelay   time.Duration
	closeHooks      runtimeCloseHooks
	savedSuspend    *runtimepkg.SavedSuspendState
	watchers        executor.Group

	state   *runtimepkg.State
	closer  *runtimepkg.Closer
	control *runtimepkg.ControlServer
}

func newRuntime(manager *manager, manifest *manifest.Manifest, paths RuntimePaths, cid int, stats *launchStats, qmp qmpClient, suspendRequests *launchSuspendCoordinator) *Runtime {
	state := runtimepkg.NewState(RuntimeStarting)
	return &Runtime{
		manager:         manager,
		manifest:        manifest,
		paths:           paths,
		cid:             cid,
		stats:           stats,
		qmp:             qmpclient.Serialized(qmp),
		suspendRequests: suspendRequests,
		savedSuspend:    runtimepkg.NewSavedSuspendState(),
		state:           state,
		closer:          runtimepkg.NewCloser(state),
	}
}

func (r *Runtime) SetReady() {
	runtimepkg.MarkReady(r.state)
}

func (r *Runtime) SetWatchers(watchers executor.Group) {
	r.watchers = watchers
}

func (r *Runtime) SetProcesses(processes *ProcessSet, shutdownDelay time.Duration) {
	r.processes = processes
	r.shutdownDelay = shutdownDelay
}

func (r *Runtime) SetLaunchLifecycle(plan *Plan, lifecycle *launchLifecycle, suspendHandler *launchSuspendHandler) {
	r.plan = plan
	r.lifecycle = lifecycle
	r.suspendHandler = suspendHandler
}

func (r *Runtime) SetCloseHooks(hooks runtimeCloseHooks) {
	r.closeHooks = hooks
}

func (r *Runtime) QMP() qmpClient {
	return r.qmp
}

func (r *Runtime) StartControl(ctx context.Context) error {
	controlServer, err := runtimepkg.StartControl(ctx, r.paths.ControlSocket, r, r.manager.logger)
	r.control = controlServer
	return err
}

func (r *Runtime) Close() error {
	return r.closer.Close(runtimepkg.CloseActions{
		WriteBack:        r.closeHooks.WriteBack,
		WriteBackTimeout: r.manager.effectiveQMPCommandTimeout(),
		SkipWriteBack:    r.savedSuspend.Saved(),
		Control:          r.control,
		Processes:        r.processes,
		ShutdownDelay:    r.shutdownDelay,
		QMP:              r.qmp,
		Cleanup:          r.closeHooks.Cleanup,
		Stats:            r.closeHooks.Stats,
	})
}

func (r *Runtime) Wait(ctx context.Context, mode WaitMode) error {
	if r.plan == nil || r.lifecycle == nil || r.suspendHandler == nil || r.processes == nil {
		return failedPrecondition(fmt.Errorf("runtime foreground wait is not configured"))
	}
	plan := launch.PlanForWaitMode(r.plan, mode)
	return runtimepkg.WaitForeground(ctx, r.savedSuspend, func(ctx context.Context) error {
		return r.manager.waitForLaunchForeground(ctx, plan, r.stats, r, r.qmp, r.lifecycle, r.suspendHandler, r.processes)
	}, func(err error) bool {
		return errors.Is(err, errSavedSuspendExit)
	})
}

func (r *Runtime) Status(ctx context.Context, req StatusRequest) (StatusResponse, error) {
	_ = ctx
	return runtimepkg.Status(r.state, r.cid, StatusPaths{
		ControlSocket:    r.paths.ControlSocket,
		QMPSocket:        r.paths.QMPSocket,
		GuestAgentSocket: r.paths.GuestAgentSocket,
		SSHReadySocket:   r.paths.SSHReadySocket,
	}, r.stats), nil
}

func (r *Runtime) Info(ctx context.Context, req InfoRequest) (InfoResponse, error) {
	resp, err := runtimepkg.Info(ctx, runtimeInfoCollector{runtime: r})
	if err != nil {
		return InfoResponse{}, failedPrecondition(err)
	}
	return resp, nil
}

type runtimeInfoCollector struct {
	runtime *Runtime
}

func (c runtimeInfoCollector) CollectInfo(ctx context.Context) (runtimepkg.GuestInfo, error) {
	info, err := c.runtime.manager.collectGuestInfo(ctx, c.runtime.paths.GuestAgentSocket, c.runtime.watchers)
	if err != nil {
		return runtimepkg.GuestInfo{}, err
	}
	return runtimepkg.GuestInfo{ProcessList: info.ProcessList}, nil
}

func (r *Runtime) Suspend(ctx context.Context, req SuspendRequest) (SuspendResponse, error) {
	if r.suspendRequests == nil {
		return SuspendResponse{}, failedPrecondition(fmt.Errorf("suspend handler is not ready"))
	}
	if err := runtimepkg.QueueSuspend(ctx, r.state, r.suspendRequests, func(err error) bool {
		return errors.Is(err, errSavedSuspendExit)
	}); err != nil {
		return SuspendResponse{}, err
	}
	return SuspendResponse{Saved: true, VMStatePath: vmStatePath(r.manifest)}, nil
}

func (r *Runtime) Balloon(ctx context.Context, req BalloonRequest) (BalloonResponse, error) {
	resp, err := runtimepkg.Balloon(ctx, r.manifest.QEMU.Devices.Balloon, r.qmp, r.manager.effectiveQMPCommandTimeout(), req)
	if errors.Is(err, runtimepkg.ErrBalloonNotConfigured) {
		return BalloonResponse{}, failedPrecondition(err)
	}
	return resp, err
}
