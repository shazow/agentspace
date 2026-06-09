package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
	"github.com/shazow/agentspace/virtie/internal/executor"
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
	server          *Server

	stateMu   sync.Mutex
	state     RuntimeState
	closeOnce sync.Once
}

func newRuntime(manager *manager, manifest *manifest.Manifest, paths RuntimePaths, cid int, stats *launchStats, qmp qmpClient, suspendRequests *launchSuspendCoordinator) *Runtime {
	return &Runtime{
		manager:         manager,
		manifest:        manifest,
		paths:           paths,
		cid:             cid,
		stats:           stats,
		qmp:             qmpclient.Serialized(qmp),
		suspendRequests: suspendRequests,
		state:           RuntimeStarting,
	}
}

func (r *Runtime) SetReady() {
	r.setState(RuntimeReady)
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
	if r.paths.ControlSocket == "" {
		return nil
	}
	listener, err := Listen(r.paths.ControlSocket)
	if err != nil {
		return err
	}
	router, err := NewRuntimeRouter(r)
	if err != nil {
		_ = listener.Close()
		return err
	}
	server := &Server{Handler: router, Logger: r.manager.logger}
	r.server = server
	go func() {
		if err := server.Serve(listener); err != nil && ctx.Err() == nil && r.manager.logger != nil {
			r.manager.logger.Info("control socket stopped", "err", err)
		}
	}()
	return nil
}

func (r *Runtime) Close() error {
	var err error
	r.closeOnce.Do(func() {
		r.setState(RuntimeStopping)
		if r.closeHooks.WriteBack != nil && !r.savedSuspend {
			writeBackCtx, cancelWriteBack := context.WithTimeout(context.Background(), r.manager.effectiveQMPCommandTimeout())
			err = errors.Join(err, r.closeHooks.WriteBack(writeBackCtx))
			cancelWriteBack()
		}
		if r.server != nil {
			err = errors.Join(err, r.server.Close())
		}
		if r.processes != nil {
			err = errors.Join(err, r.processes.Close(r.shutdownDelay))
		}
		if r.qmp != nil {
			err = errors.Join(err, r.qmp.Disconnect())
		}
		if r.closeHooks.Cleanup != nil {
			err = errors.Join(err, r.closeHooks.Cleanup())
		}
		if r.closeHooks.Stats != nil {
			r.closeHooks.Stats()
		}
		r.setState(RuntimeStopped)
	})
	return err
}

func (r *Runtime) Wait(ctx context.Context, mode WaitMode) error {
	if r.plan == nil || r.lifecycle == nil || r.suspendHandler == nil || r.processes == nil {
		return failedPrecondition(fmt.Errorf("runtime foreground wait is not configured"))
	}
	plan := r.plan
	if mode == WaitSSH || mode == WaitVM {
		copyPlan := *r.plan
		copyOptions := copyPlan.Options
		copyOptions.SSH = mode == WaitSSH
		copyPlan.Options = copyOptions
		plan = &copyPlan
	}
	err := r.manager.waitForLaunchForeground(ctx, plan, r.stats, r, r.qmp, r.lifecycle, r.suspendHandler, r.processes)
	if errors.Is(err, errSavedSuspendExit) {
		r.savedSuspend = true
	}
	return err
}

func (r *Runtime) Status(ctx context.Context, req StatusRequest) (StatusResponse, error) {
	_ = ctx
	return StatusResponse{
		State: r.currentState(),
		CID:   r.cid,
		Paths: StatusPaths{
			ControlSocket:    r.paths.ControlSocket,
			QMPSocket:        r.paths.QMPSocket,
			GuestAgentSocket: r.paths.GuestAgentSocket,
			SSHReadySocket:   r.paths.SSHReadySocket,
		},
		Stats: runtimeStatsFromLaunchStats(r.stats),
	}, nil
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
	r.setState(RuntimeSuspending)
	err := r.suspendRequests.RequestAndWait(ctx)
	if err != nil && !errors.Is(err, errSavedSuspendExit) {
		return SuspendResponse{}, err
	}
	r.setState(RuntimeSuspended)
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

func failedPrecondition(err error) error {
	return &RPCError{Code: ErrFailedPrecondition, Message: err.Error()}
}

func (r *Runtime) setState(state RuntimeState) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.state = state
}

func (r *Runtime) currentState() RuntimeState {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.state
}

func runtimeStatsFromLaunchStats(stats *launchStats) RuntimeStats {
	if stats == nil {
		return RuntimeStats{}
	}
	resp := RuntimeStats{
		StartedAt:     stats.started,
		BootStartedAt: stats.bootStarted,
		QMPReadyAt:    stats.qmpReady,
		FilesReadyAt:  stats.filesReady,
		SSHReadyAt:    stats.sshReady,
		SSHStartedAt:  stats.sshStarted,
		CompletedAt:   stats.completed,
		SSHAttempts:   stats.sshAttempts,
	}
	if !stats.started.IsZero() && !stats.bootStarted.IsZero() {
		resp.StartedToBoot = stats.bootStarted.Sub(stats.started).String()
	}
	if !stats.bootStarted.IsZero() && !stats.qmpReady.IsZero() {
		resp.BootToQMP = stats.qmpReady.Sub(stats.bootStarted).String()
	}
	sshReady := stats.sshReady
	if sshReady.IsZero() {
		sshReady = stats.sshStarted
	}
	if !stats.filesReady.IsZero() && !sshReady.IsZero() {
		resp.FilesToSSH = sshReady.Sub(stats.filesReady).String()
	}
	if !stats.bootStarted.IsZero() && !stats.completed.IsZero() {
		resp.BootToCompleted = stats.completed.Sub(stats.bootStarted).String()
	}
	if !stats.started.IsZero() && !stats.completed.IsZero() {
		resp.Total = stats.completed.Sub(stats.started).String()
	}
	return resp
}

func isControlSocketUnavailable(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

func isControlUnsupported(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == ErrUnsupported
}
