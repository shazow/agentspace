package balloon

import "testing"

func TestApplyDefaultsCreatesHalfAllocationTarget(t *testing.T) {
	device := &Device{
		ID:        "balloon0",
		Transport: "pci",
	}

	ApplyDefaults(2048, device)

	if device.Controller == nil {
		t.Fatal("expected controller defaults to be created")
	}
	if got, want := device.Controller.MinActualMiB, 1024; got != want {
		t.Fatalf("unexpected minActualMiB: got %d want %d", got, want)
	}
	if got, want := device.Controller.MaxActualMiB, 2048; got != want {
		t.Fatalf("unexpected maxActualMiB: got %d want %d", got, want)
	}
	if got, want := device.Controller.GrowBelowAvailableMiB, 512; got != want {
		t.Fatalf("unexpected growBelowAvailableMiB: got %d want %d", got, want)
	}
	if got, want := device.Controller.ReclaimAboveAvailableMiB, 1024; got != want {
		t.Fatalf("unexpected reclaimAboveAvailableMiB: got %d want %d", got, want)
	}
	if got, want := device.Controller.StepMiB, DefaultControllerStepMiB; got != want {
		t.Fatalf("unexpected stepMiB: got %d want %d", got, want)
	}
	if got, want := device.Controller.PollIntervalSeconds, DefaultControllerPollIntervalSecs; got != want {
		t.Fatalf("unexpected pollIntervalSeconds: got %d want %d", got, want)
	}
	if got, want := device.Controller.ReclaimHoldoffSeconds, DefaultControllerReclaimHoldoff; got != want {
		t.Fatalf("unexpected reclaimHoldoffSeconds: got %d want %d", got, want)
	}
}

func TestApplyDefaultsDerivesThresholdsFromExplicitIdleTarget(t *testing.T) {
	device := &Device{
		ID:        "balloon0",
		Transport: "pci",
		Controller: &ControllerConfig{
			MinActualMiB: 768,
		},
	}

	ApplyDefaults(2048, device)

	if got, want := device.Controller.MaxActualMiB, 2048; got != want {
		t.Fatalf("unexpected maxActualMiB: got %d want %d", got, want)
	}
	if got, want := device.Controller.GrowBelowAvailableMiB, 384; got != want {
		t.Fatalf("unexpected growBelowAvailableMiB: got %d want %d", got, want)
	}
	if got, want := device.Controller.ReclaimAboveAvailableMiB, 768; got != want {
		t.Fatalf("unexpected reclaimAboveAvailableMiB: got %d want %d", got, want)
	}
}

func TestValidateControllerRejectsNegativeThresholds(t *testing.T) {
	config := &ControllerConfig{
		MinActualMiB:             512,
		MaxActualMiB:             1024,
		GrowBelowAvailableMiB:    -1,
		ReclaimAboveAvailableMiB: 512,
		StepMiB:                  DefaultControllerStepMiB,
		PollIntervalSeconds:      DefaultControllerPollIntervalSecs,
		ReclaimHoldoffSeconds:    DefaultControllerReclaimHoldoff,
	}
	if err := ValidateController(1024, config); err == nil {
		t.Fatal("expected negative grow threshold validation error")
	}

	config = &ControllerConfig{
		MinActualMiB:             512,
		MaxActualMiB:             1024,
		GrowBelowAvailableMiB:    256,
		ReclaimAboveAvailableMiB: -1,
		StepMiB:                  DefaultControllerStepMiB,
		PollIntervalSeconds:      DefaultControllerPollIntervalSecs,
		ReclaimHoldoffSeconds:    DefaultControllerReclaimHoldoff,
	}
	if err := ValidateController(1024, config); err == nil {
		t.Fatal("expected negative reclaim threshold validation error")
	}
}
