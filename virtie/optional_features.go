package virtie

import (
	"context"
	"log"
	"time"

	govmmQemu "github.com/kata-containers/govmm/qemu"
	"github.com/shazow/agentspace/virtie/balloon"
	manifestpkg "github.com/shazow/agentspace/virtie/manifest"
)

type qemuTransportResolver func(string) (govmmQemu.VirtioTransport, error)

type optionalFeatureRuntime struct {
	Logger     *log.Logger
	QMPTimeout time.Duration
}

type optionalFeature interface {
	AppendQEMUArgs(
		qemu manifestpkg.ManifestQEMU,
		config *govmmQemu.Config,
		resolveTransport qemuTransportResolver,
		args []string,
	) ([]string, error)
	StartTask(
		ctx context.Context,
		runtime optionalFeatureRuntime,
		manifest *manifestpkg.Manifest,
		qmpClient QMPClient,
	) *managedTask
}

type balloonFeature struct{}

var builtinOptionalFeatures = []optionalFeature{
	balloonFeature{},
}

var optionalFeatures = builtinOptionalFeatures

func appendOptionalFeatureQEMUArgs(qemu manifestpkg.ManifestQEMU, config *govmmQemu.Config, args []string) ([]string, error) {
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
	manifest *manifestpkg.Manifest,
	qmpClient QMPClient,
) managedTaskGroup {
	var tasks managedTaskGroup
	for _, feature := range optionalFeatures {
		tasks.Add(feature.StartTask(ctx, runtime, manifest, qmpClient))
	}
	return tasks
}

func (balloonFeature) AppendQEMUArgs(
	qemu manifestpkg.ManifestQEMU,
	config *govmmQemu.Config,
	resolveTransport qemuTransportResolver,
	args []string,
) ([]string, error) {
	return balloon.AppendQEMUArgs(args, config, resolveTransport, qemu.Devices.Balloon)
}

func (balloonFeature) StartTask(
	ctx context.Context,
	runtime optionalFeatureRuntime,
	manifest *manifestpkg.Manifest,
	qmpClient QMPClient,
) *managedTask {
	if manifest == nil || qmpClient == nil {
		return nil
	}

	task := balloon.ControllerTask(runtime.Logger, runtime.QMPTimeout, qmpClient, manifest.QEMU.Devices.Balloon)
	if task == nil {
		return nil
	}
	return startManagedTask(ctx, task)
}
