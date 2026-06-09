package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
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
	savedSuspend    bool
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
		SkipWriteBack:    r.savedSuspend,
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
	err := r.manager.waitForLaunchForeground(ctx, plan, r.stats, r, r.qmp, r.lifecycle, r.suspendHandler, r.processes)
	if errors.Is(err, errSavedSuspendExit) {
		r.savedSuspend = true
	}
	return err
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
	info, err := r.manager.collectGuestInfo(ctx, r.paths.GuestAgentSocket, r.watchers)
	if err != nil {
		return InfoResponse{}, failedPrecondition(err)
	}
	return InfoResponse{ProcessList: info.ProcessList}, nil
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
	_ = ctx
	if r.manifest.QEMU.Devices.Balloon == nil {
		return BalloonResponse{}, failedPrecondition(fmt.Errorf("balloon device is not configured"))
	}
	timeout := r.manager.effectiveQMPCommandTimeout()
	var actual int64
	if err := r.qmp.WithRaw(timeout, func(monitor *rawQMP.Monitor) error {
		info, err := monitor.QueryBalloon()
		if err != nil {
			return fmt.Errorf("qmp query-balloon: %w", err)
		}
		actual = info.Actual
		if req.TargetBytes > 0 {
			if err := monitor.Balloon(req.TargetBytes); err != nil {
				return fmt.Errorf("qmp balloon: %w", err)
			}
		}
		return nil
	}); err != nil {
		return BalloonResponse{}, err
	}
	return BalloonResponse{ActualBytes: actual, TargetBytes: req.TargetBytes}, nil
}
