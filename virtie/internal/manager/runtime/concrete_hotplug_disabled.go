//go:build virtie_no_hotplug

package runtime

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

func (r *Runtime) Hotplug(ctx context.Context, req control.HotplugRequest) (control.HotplugResponse, error) {
	return control.HotplugResponse{}, unsupportedHotplug()
}
