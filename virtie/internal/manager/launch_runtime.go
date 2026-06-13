package manager

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/executor"
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
)

type launchRuntime struct {
	manager *manager
}

func (r launchRuntime) New(spec launch.RuntimeSpec) (launch.RuntimeResult, error) {
	deps := runtimepkg.Dependencies{
		QMPTimeout:       r.manager.effectiveQMPCommandTimeout(),
		Logger:           r.manager.logger,
		SavedSuspendExit: launch.IsSavedSuspendExit,
		CollectInfo: func(ctx context.Context, socketPath string, watchers executor.Group) (runtimepkg.GuestInfo, error) {
			info, err := r.manager.collectGuestInfo(ctx, socketPath, watchers)
			if err != nil {
				return runtimepkg.GuestInfo{}, err
			}
			return runtimepkg.GuestInfo{ProcessList: info.ProcessList}, nil
		},
	}
	core := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest:        spec.Manifest,
		Plan:            spec.Plan,
		Paths:           spec.Paths,
		CID:             spec.CID,
		Stats:           spec.Stats,
		QMP:             spec.QMP,
		SuspendRequests: spec.SuspendRequests,
		Processes:       spec.Processes,
		ShutdownDelay:   r.manager.shutdownDelay,
		WaitForeground:  spec.WaitForeground,
		CloseHooks: runtimepkg.CloseHooks{
			WriteBack: spec.WriteBack,
			Cleanup:   spec.Cleanup,
			Stats:     spec.CloseStats,
		},
		Dependencies: deps,
	})
	return launch.RuntimeResult{
		Runtime: core,
		ControlOptions: []controlpkg.RouterOption{
			controlpkg.WithHotplug(r.manager.hotplugFeature(spec.Manifest, core.QMP())),
		},
	}, nil
}

func (r launchRuntime) SuspendHandler(spec launch.SuspendSpec) launch.SuspendHandler {
	handler := newLaunchSuspendHandler(r.manager, spec.Manifest, spec.Plan.Paths.QMPSocket, spec.QMP, spec.CID, spec.Plan.Notifier, spec.WriteBackOnExit)
	return launchSuspendHandlerAdapter{handler: handler}
}

func (r launchRuntime) WaitForeground(spec launch.ForegroundSpec) func(context.Context, *launch.Plan) error {
	return func(ctx context.Context, waitPlan *launch.Plan) error {
		return r.manager.waitForLaunchForeground(ctx, waitPlan, spec.Stats, spec.Runtime(), spec.QMP, spec.Lifecycle, spec.SuspendHandler, spec.Processes)
	}
}

type launchSuspendHandlerAdapter struct {
	handler *launchSuspendHandler
}

func (a launchSuspendHandlerAdapter) Handle(ctx context.Context, coordinator *launch.SuspendCoordinator) error {
	return handleSuspendRequest(ctx, coordinator, a.handler)
}
