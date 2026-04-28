package manager

import (
	"context"
	"log"
	"time"

	govmmQemu "github.com/kata-containers/govmm/qemu"
	"github.com/shazow/agentspace/virtie/internal/balloon"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type qemuTransportResolver func(string) (govmmQemu.VirtioTransport, error)

type optionalFeatureRuntime struct {
	logger     *log.Logger
	qmpTimeout time.Duration
	notifier   notificationSink
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

type balloonFeature struct{}

var builtinOptionalFeatures = []optionalFeature{
	balloonFeature{},
}

var optionalFeatures = builtinOptionalFeatures

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

func (balloonFeature) AppendQEMUArgs(
	qemu manifest.QEMU,
	config *govmmQemu.Config,
	resolveTransport qemuTransportResolver,
	args []string,
) ([]string, error) {
	return balloon.AppendQEMUArgs(args, config, resolveTransport, qemu.Devices.Balloon)
}

func (balloonFeature) StartTask(
	ctx context.Context,
	runtime optionalFeatureRuntime,
	manifest *manifest.Manifest,
	qmpClient qmpClient,
) *managedTask {
	if manifest == nil || qmpClient == nil {
		return nil
	}

	task := balloon.ControllerTask(runtime.logger, runtime.qmpTimeout, qmpClient, manifest.QEMU.Devices.Balloon, runtime.notifier)
	if task == nil {
		return nil
	}
	return startManagedTask(ctx, task)
}
