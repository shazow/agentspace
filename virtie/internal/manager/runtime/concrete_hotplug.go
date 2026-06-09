//go:build !virtie_no_hotplug

package runtime

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/hotplug"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
)

func (r *Runtime) Hotplug(ctx context.Context, req control.HotplugRequest) (control.HotplugResponse, error) {
	runtime := hotplug.Runtime{
		StateDir: r.manifest.ResolvedPersistenceStateDir(),
		WorkDir:  r.manifest.Paths.WorkingDir,
		Devices:  r.manifest.Hotplug,
		Start:    r.hotplugStart,
		Sockets:  r.hotplugSockets,
		QMP:      HotplugQMP{Client: r.qmp, Timeout: r.qmpTimeout},
		Guest:    r.hotplugGuest,
	}
	resp, err := Hotplug(ctx, runtime, req)
	if err != nil {
		return control.HotplugResponse{}, launch.WrapHotplugError(err)
	}
	return resp, nil
}
