package runtime

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type HotplugRuntime interface {
	Attach(context.Context, string) error
	Detach(context.Context, string) error
}

func Hotplug(ctx context.Context, runtime HotplugRuntime, req control.HotplugRequest) (control.HotplugResponse, error) {
	if req.Detach {
		if err := runtime.Detach(ctx, req.ID); err != nil {
			return control.HotplugResponse{}, err
		}
		return control.HotplugResponse{ID: req.ID, Detach: true}, nil
	}
	if err := runtime.Attach(ctx, req.ID); err != nil {
		return control.HotplugResponse{}, err
	}
	return control.HotplugResponse{ID: req.ID}, nil
}
