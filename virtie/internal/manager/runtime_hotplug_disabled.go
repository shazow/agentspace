//go:build virtie_no_hotplug

package manager

import "context"

func (r *Runtime) Hotplug(ctx context.Context, req HotplugRequest) (HotplugResponse, error) {
	return HotplugResponse{}, &RPCError{Code: ErrUnsupported, Message: "hotplug support is not built into this virtie binary"}
}
