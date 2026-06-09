package manager

import (
	"context"
	"time"

	govmmQemu "github.com/kata-containers/govmm/qemu"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type qemuTransportResolver func(string) (govmmQemu.VirtioTransport, error)

type optionalFeatureRuntime struct {
	qmpTimeout time.Duration
	notifier   launch.NotificationSink
}

type optionalFeature interface {
	AppendQEMUArgs(
		qemu manifest.QEMU,
		config *govmmQemu.Config,
		resolveTransport qemuTransportResolver,
		args []string,
	) ([]string, error)
	StartTask(
		ctx context.Context,
		runtime optionalFeatureRuntime,
		manifest *manifest.Manifest,
		qmpClient qmpClient,
	) *managedTask
}

var optionalFeatures []optionalFeature

func appendOptionalFeatureQEMUArgs(qemu manifest.QEMU, config *govmmQemu.Config, args []string) ([]string, error) {
	var err error
	for _, feature := range optionalFeatures {
		args, err = feature.AppendQEMUArgs(qemu, config, resolveQEMUTransport, args)
		if err != nil {
			return nil, err
		}
	}
	return args, nil
}

func startOptionalFeatureTasks(
	ctx context.Context,
	runtime optionalFeatureRuntime,
	manifest *manifest.Manifest,
	qmpClient qmpClient,
) managedTaskGroup {
	var tasks managedTaskGroup
	for _, feature := range optionalFeatures {
		tasks.Add(feature.StartTask(ctx, runtime, manifest, qmpClient))
	}
	return tasks
}
