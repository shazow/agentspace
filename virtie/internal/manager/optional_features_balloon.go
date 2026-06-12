//go:build !virtie_no_balloon

package manager

import (
	"context"

	govmmQemu "github.com/kata-containers/govmm/qemu"
	"github.com/shazow/agentspace/virtie/internal/balloon"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
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
	qmpClient qmpclient.Client,
) func(context.Context) error {
	if manifest == nil || qmpClient == nil {
		return nil
	}

	return balloon.ControllerTask(runtime.qmpTimeout, qmpClient, manifest.QEMU.Devices.Balloon, runtime.notifier)
}
