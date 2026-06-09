//go:build !virtie_no_hotplug

package manager

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/hotplug"
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
)

func (r *Runtime) Hotplug(ctx context.Context, req controlpkg.HotplugRequest) (controlpkg.HotplugResponse, error) {
	runtime := hotplug.Runtime{
		StateDir: r.manifest.ResolvedPersistenceStateDir(),
		WorkDir:  r.manifest.Paths.WorkingDir,
		Devices:  r.manifest.Hotplug,
		Start:    r.hotplugStart,
		Sockets:  r.hotplugSockets,
		QMP:      managerHotplugQMP{client: r.qmp, timeout: r.qmpTimeout},
		Guest:    r.hotplugGuest,
	}
	resp, err := runtimepkg.Hotplug(ctx, runtime, req)
	if err != nil {
		return controlpkg.HotplugResponse{}, launch.WrapHotplugError(err)
	}
	return resp, nil
}
