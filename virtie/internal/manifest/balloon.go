package manifest

import (
	"fmt"

	"github.com/shazow/agentspace/virtie/internal/balloon"
)

func applyBalloonDefaults(memoryMiB int, device *balloon.Device) {
	balloon.ApplyDefaults(memoryMiB, device)
}

func validateBalloonDevice(memoryMiB int, device *balloon.Device) error {
	if device == nil {
		return nil
	}

	switch {
	case device.ID == "":
		return fmt.Errorf("manifest.qemu.devices.balloon.id is required")
	case !validQEMUTransport(device.Transport):
		return fmt.Errorf("manifest.qemu.devices.balloon.transport must be one of pci, mmio, or ccw")
	}

	if err := balloon.ValidateController(memoryMiB, device.Controller); err != nil {
		return fmt.Errorf("manifest.qemu.devices.balloon.controller.%s", err)
	}

	return nil
}

func cloneBalloonDevice(device *balloon.Device) *balloon.Device {
	return balloon.CloneDevice(device)
}
