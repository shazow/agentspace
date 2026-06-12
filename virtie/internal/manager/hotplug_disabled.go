//go:build virtie_no_hotplug

package manager

import (
	"context"
	"fmt"

	"github.com/shazow/agentspace/virtie/internal/hotplugtypes"
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
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
		return &launch.StageError{Stage: "preflight", Err: err}
	}
	controlSocketPath, err := launchManifest.ResolvedControlSocketPath()
	if err == nil && controlSocketPath != "" {
		_, err := controlpkg.Dial(controlSocketPath).Hotplug(ctx, controlpkg.HotplugRequest{ID: id, Detach: options.Detach})
		if err == nil {
			return nil
		}
		if !controlpkg.IsSocketUnavailable(err) {
			return &launch.StageError{Stage: "control hotplug", Err: err}
		}
	}
	return &launch.StageError{Stage: "hotplug", Err: fmt.Errorf("hotplug support is not built into this virtie binary")}
}

func configureRuntimeHotplugDependencies(deps *runtimepkg.Dependencies, m *manager, launchManifest *manifest.Manifest) {
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
