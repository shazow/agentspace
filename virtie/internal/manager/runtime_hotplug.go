//go:build !virtie_no_hotplug

package manager

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/hotplug"
)

func (r *Runtime) Hotplug(ctx context.Context, req HotplugRequest) (HotplugResponse, error) {
	runtime := hotplug.Runtime{
		StateDir: r.manifest.ResolvedPersistenceStateDir(),
		WorkDir:  r.manifest.Paths.WorkingDir,
		Devices:  r.manifest.Hotplug,
		Start:    managerHotplugStarter{m: r.manager},
		Sockets:  managerHotplugSocketWaiter{m: r.manager},
		QMP:      managerHotplugQMP{client: r.qmp, timeout: r.manager.effectiveQMPCommandTimeout()},
		Guest:    managerHotplugGuest{m: r.manager, manifest: r.manifest},
	}
	if req.Detach {
		if err := runtime.Detach(ctx, req.ID); err != nil {
			return HotplugResponse{}, wrapHotplugError(err)
		}
		return HotplugResponse{ID: req.ID, Detach: true}, nil
	}
	if err := runtime.Attach(ctx, req.ID); err != nil {
		return HotplugResponse{}, wrapHotplugError(err)
	}
	return HotplugResponse{ID: req.ID}, nil
}
