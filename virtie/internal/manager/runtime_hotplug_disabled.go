//go:build virtie_no_hotplug

package manager

import (
	"context"

	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
)

func (r *Runtime) Hotplug(ctx context.Context, req controlpkg.HotplugRequest) (controlpkg.HotplugResponse, error) {
	return controlpkg.HotplugResponse{}, runtimepkg.UnsupportedHotplug()
}
