//go:build !virtie_no_balloon

// Package balloon implements the internal virtio-balloon feature.
//
// It owns the manifest-facing balloon configuration, the QEMU argument lowering
// for the virtio-balloon device, and the optional runtime controller that
// adjusts guest memory through QMP. The controller reads guest pressure stats,
// applies the configured hysteresis and holdoff rules, and issues balloon
// commands without exposing those QMP-specific details to the rest of virtie.
package balloon

import (
	"github.com/shazow/agentspace/virtie/internal/balloontypes"
	"github.com/shazow/agentspace/virtie/internal/units"
)

const (
	bytesPerMiB int64 = 1024 * 1024

	defaultControllerStep             = balloontypes.DefaultControllerStep
	defaultControllerPollIntervalSecs = balloontypes.DefaultControllerPollIntervalSecs
	defaultControllerReclaimHoldoff   = balloontypes.DefaultControllerReclaimHoldoff
)

type Device = balloontypes.Device

type ControllerConfig = balloontypes.ControllerConfig

func ApplyDefaults(memory units.MiB, device *Device) {
	balloontypes.ApplyDefaults(memory, device)
}

func ValidateController(memory units.MiB, controller *ControllerConfig) error {
	return balloontypes.ValidateController(memory, controller)
}

func CloneDevice(device *Device) *Device {
	return balloontypes.CloneDevice(device)
}
