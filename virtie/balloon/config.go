package balloon

import "fmt"

const (
	BytesPerMiB int64 = 1024 * 1024

	DefaultControllerStepMiB          = 256
	DefaultControllerPollIntervalSecs = 5
	DefaultControllerReclaimHoldoff   = 30
)

type Device struct {
	ID                string            `json:"id"`
	Transport         string            `json:"transport"`
	DeflateOnOOM      bool              `json:"deflateOnOOM,omitempty"`
	FreePageReporting bool              `json:"freePageReporting,omitempty"`
	Controller        *ControllerConfig `json:"controller,omitempty"`
}

type ControllerConfig struct {
	MinActualMiB             int `json:"minActualMiB"`
	MaxActualMiB             int `json:"maxActualMiB,omitempty"`
	GrowBelowAvailableMiB    int `json:"growBelowAvailableMiB"`
	ReclaimAboveAvailableMiB int `json:"reclaimAboveAvailableMiB"`
	StepMiB                  int `json:"stepMiB,omitempty"`
	PollIntervalSeconds      int `json:"pollIntervalSeconds,omitempty"`
	ReclaimHoldoffSeconds    int `json:"reclaimHoldoffSeconds,omitempty"`
}

func ApplyDefaults(memoryMiB int, device *Device) {
	if device == nil {
		return
	}

	if device.Controller == nil {
		device.Controller = &ControllerConfig{}
	}

	controller := device.Controller
	if controller.MaxActualMiB == 0 {
		controller.MaxActualMiB = memoryMiB
	}
	idleTargetMiB := controller.MinActualMiB
	if idleTargetMiB <= 0 {
		idleTargetMiB = defaultMinActualMiB(controller.MaxActualMiB, memoryMiB)
	}
	if controller.MinActualMiB == 0 {
		controller.MinActualMiB = idleTargetMiB
	}
	if controller.GrowBelowAvailableMiB == 0 {
		controller.GrowBelowAvailableMiB = defaultGrowBelowAvailableMiB(idleTargetMiB)
	}
	if controller.ReclaimAboveAvailableMiB == 0 {
		controller.ReclaimAboveAvailableMiB = defaultReclaimAboveAvailableMiB(idleTargetMiB)
	}
	if controller.StepMiB == 0 {
		controller.StepMiB = DefaultControllerStepMiB
	}
	if controller.PollIntervalSeconds == 0 {
		controller.PollIntervalSeconds = DefaultControllerPollIntervalSecs
	}
	if controller.ReclaimHoldoffSeconds == 0 {
		controller.ReclaimHoldoffSeconds = DefaultControllerReclaimHoldoff
	}
}

func defaultMinActualMiB(maxActualMiB int, fallbackMiB int) int {
	if maxActualMiB <= 0 {
		maxActualMiB = fallbackMiB
	}
	if maxActualMiB <= 1 {
		return 1
	}
	return (maxActualMiB + 1) / 2
}

func defaultGrowBelowAvailableMiB(minActualMiB int) int {
	if minActualMiB <= 1 {
		return 0
	}
	return minActualMiB / 2
}

func defaultReclaimAboveAvailableMiB(minActualMiB int) int {
	if minActualMiB <= 0 {
		return 1
	}
	return minActualMiB
}

func ValidateController(memoryMiB int, controller *ControllerConfig) error {
	if controller == nil {
		return nil
	}

	switch {
	case controller.MinActualMiB <= 0:
		return fmt.Errorf("minActualMiB must be greater than zero")
	case controller.MinActualMiB > controller.MaxActualMiB:
		return fmt.Errorf("minActualMiB must be less than or equal to maxActualMiB")
	case controller.MaxActualMiB > memoryMiB:
		return fmt.Errorf("maxActualMiB must be less than or equal to manifest.qemu.memory.sizeMiB")
	case controller.GrowBelowAvailableMiB < 0:
		return fmt.Errorf("growBelowAvailableMiB must be greater than or equal to zero")
	case controller.ReclaimAboveAvailableMiB < 0:
		return fmt.Errorf("reclaimAboveAvailableMiB must be greater than or equal to zero")
	case controller.GrowBelowAvailableMiB >= controller.ReclaimAboveAvailableMiB:
		return fmt.Errorf("growBelowAvailableMiB must be less than reclaimAboveAvailableMiB")
	case controller.StepMiB <= 0:
		return fmt.Errorf("stepMiB must be greater than zero")
	case controller.PollIntervalSeconds <= 0:
		return fmt.Errorf("pollIntervalSeconds must be greater than zero")
	case controller.ReclaimHoldoffSeconds <= 0:
		return fmt.Errorf("reclaimHoldoffSeconds must be greater than zero")
	}

	return nil
}

func CloneDevice(device *Device) *Device {
	if device == nil {
		return nil
	}

	cloned := *device
	if device.Controller != nil {
		controller := *device.Controller
		cloned.Controller = &controller
	}
	return &cloned
}
