package manager

import (
	"context"
	"time"

	govmmQemu "github.com/kata-containers/govmm/qemu"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
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
		qmpClient qmpclient.Client,
	) func(context.Context) error
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
	qmpClient qmpclient.Client,
) []func(context.Context) error {
	var tasks []func(context.Context) error
	for _, feature := range optionalFeatures {
		if task := feature.StartTask(ctx, runtime, manifest, qmpClient); task != nil {
			tasks = append(tasks, task)
		}
	}
	return tasks
}
