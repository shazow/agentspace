//go:build virtie_no_hotplug

package manager

import (
	"context"

	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
)

func (r *Runtime) Hotplug(ctx context.Context, req HotplugRequest) (HotplugResponse, error) {
	return HotplugResponse{}, runtimepkg.UnsupportedHotplug()
}
