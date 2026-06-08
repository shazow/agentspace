package manifest

import (
	"fmt"

	"github.com/shazow/agentspace/virtie/internal/balloontypes"
	"github.com/shazow/agentspace/virtie/internal/units"
)

func applyBalloonDefaults(memory units.MiB, device *balloontypes.Device) {
	balloontypes.ApplyDefaults(memory, device)
}

func validateBalloonDevice(memory units.MiB, device *balloontypes.Device) error {
	if device == nil {
		return nil
	}

	switch {
	case !validQEMUTransport(device.Transport):
		return fmt.Errorf("manifest.qemu.devices.balloon.transport must be one of pci, mmio, or ccw")
	}

	if err := balloontypes.ValidateController(memory, device.Controller); err != nil {
		return fmt.Errorf("manifest.qemu.devices.balloon.controller.%s", err)
	}

	return nil
}

func cloneBalloonDevice(device *balloontypes.Device) *balloontypes.Device {
	return balloontypes.CloneDevice(device)
}
