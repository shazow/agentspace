//go:build !virtie_no_hotplug

package runtime

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/hotplug"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
)

func (r *Runtime) Hotplug(ctx context.Context, req control.HotplugRequest) (control.HotplugResponse, error) {
	hotplugRuntime := hotplug.Runtime{
		StateDir: r.manifest.ResolvedPersistenceStateDir(),
		WorkDir:  r.manifest.Paths.WorkingDir,
		Devices:  r.manifest.Hotplug,
		Start:    r.hotplugStart,
		Sockets:  r.hotplugSockets,
		QMP:      HotplugQMP{Client: r.qmp, Timeout: r.qmpTimeout},
		Guest:    r.hotplugGuest,
	}
	if req.Detach {
		if err := hotplugRuntime.Detach(ctx, req.ID); err != nil {
			return control.HotplugResponse{}, launch.WrapHotplugError(err)
		}
		return control.HotplugResponse{ID: req.ID, Detach: true}, nil
	}
	if err := hotplugRuntime.Attach(ctx, req.ID); err != nil {
		return control.HotplugResponse{}, launch.WrapHotplugError(err)
	}
	return control.HotplugResponse{ID: req.ID}, nil
}
