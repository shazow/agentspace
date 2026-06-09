//go:build !virtie_no_hotplug

package manager

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/hotplug"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
)

func (r *Runtime) Hotplug(ctx context.Context, req HotplugRequest) (HotplugResponse, error) {
	runtime := hotplug.Runtime{
		StateDir: r.manifest.ResolvedPersistenceStateDir(),
		WorkDir:  r.manifest.Paths.WorkingDir,
		Devices:  r.manifest.Hotplug,
		Start:    managerHotplugStarter{m: r.manager},
		Sockets:  managerHotplugSocketWaiter{m: r.manager},
		QMP:      managerHotplugQMP{client: r.qmp, timeout: r.qmpTimeout},
		Guest:    managerHotplugGuest{m: r.manager, manifest: r.manifest},
	}
	resp, err := runtimepkg.Hotplug(ctx, runtime, req)
	if err != nil {
		return HotplugResponse{}, wrapHotplugError(err)
	}
	return resp, nil
}
