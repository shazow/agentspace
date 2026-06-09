package runtime

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type GuestInfo struct {
	ProcessList string
}

type InfoCollector interface {
	CollectInfo(context.Context) (GuestInfo, error)
}

func Info(ctx context.Context, collector InfoCollector) (control.InfoResponse, error) {
	info, err := collector.CollectInfo(ctx)
	if err != nil {
		return control.InfoResponse{}, err
	}
	return control.InfoResponse{ProcessList: info.ProcessList}, nil
}

func ControlInfo(ctx context.Context, collector InfoCollector) (control.InfoResponse, error) {
	resp, err := Info(ctx, collector)
	if err != nil {
		return control.InfoResponse{}, control.FailedPrecondition(err)
	}
	return resp, nil
}
