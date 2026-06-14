package manager

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/executor"
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
)

type managerInfoFeature struct {
	manager    *manager
	socketPath string
	processes  *launch.ProcessSet
}

func (m *manager) infoFeature(socketPath string, processes *launch.ProcessSet) managerInfoFeature {
	return managerInfoFeature{manager: m, socketPath: socketPath, processes: processes}
}

func (f managerInfoFeature) Info(ctx context.Context, req controlpkg.InfoRequest) (controlpkg.InfoResponse, error) {
	_ = req
	watchers := executor.Group{}
	if f.processes != nil {
		watchers = f.processes.Watchers()
	}
	info, err := f.manager.collectGuestInfo(ctx, f.socketPath, watchers)
	if err != nil {
		return controlpkg.InfoResponse{}, controlpkg.FailedPrecondition(err)
	}
	return controlpkg.InfoResponse{ProcessList: info.ProcessList}, nil
}
