//go:build virtie_no_hotplug

package manager

import (
	"context"
	"fmt"

	"github.com/shazow/agentspace/virtie/internal/hotplugtypes"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type HotplugOptions struct {
	Detach bool
}

const hotplugBuiltIn = false

func Hotplug(ctx context.Context, manifest *manifest.Manifest, id string, options HotplugOptions) error {
	return newManager().hotplug(ctx, manifest, id, options)
}

func (m *manager) hotplug(ctx context.Context, launchManifest *manifest.Manifest, id string, options HotplugOptions) error {
	if err := launchManifest.Validate(); err != nil {
		return &stageError{Stage: "preflight", Err: err}
	}
	controlSocketPath, err := launchManifest.ResolvedControlSocketPath()
	if err == nil && controlSocketPath != "" {
		_, err := Dial(controlSocketPath).Hotplug(ctx, HotplugRequest{ID: id, Detach: options.Detach})
		if err == nil {
			return nil
		}
		if !isControlSocketUnavailable(err) {
			return &stageError{Stage: "control hotplug", Err: err}
		}
	}
	return &stageError{Stage: "hotplug", Err: fmt.Errorf("hotplug support is not built into this virtie binary")}
}

func configureRuntimeHotplugDependencies(deps *runtimeDependencies, m *manager, launchManifest *manifest.Manifest) {
}

func hotplugStatePath(launchManifest *manifest.Manifest, id string) (string, error) {
	return hotplugtypes.StatePath(launchManifest.ResolvedPersistenceStateDir(), id)
}

func writeHotplugState(path string, state hotplugtypes.State) error {
	return hotplugtypes.WriteState(path, state)
}

func readHotplugState(path string) (hotplugtypes.State, error) {
	return hotplugtypes.ReadState(path)
}
