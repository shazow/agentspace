//go:build !virtie_no_balloon

package manager

import (
	"context"

	govmmQemu "github.com/kata-containers/govmm/qemu"
	"github.com/shazow/agentspace/virtie/internal/balloon"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type balloonFeature struct{}

func init() {
	optionalFeatures = append(optionalFeatures, balloonFeature{})
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

	task := balloon.ControllerTask(runtime.qmpTimeout, qmpClient, manifest.QEMU.Devices.Balloon, runtime.notifier)
	if task == nil {
		return nil
	}
	return startManagedTask(ctx, task)
}
