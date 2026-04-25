package manifest

import (
	"testing"

	"github.com/shazow/agentspace/virtie/internal/balloon"
)

func TestValidateAppliesBalloonDefaults(t *testing.T) {
	manifest := validManifestWithBalloon()

	if err := manifest.Validate(); err != nil {
		t.Fatalf("unexpected balloon controller validation error: %v", err)
	}
	if manifest.QEMU.Devices.Balloon.Controller == nil {
		t.Fatal("expected balloon controller defaults to be synthesized")
	}
	if got, want := manifest.QEMU.Devices.Balloon.Controller.MinActualMiB, 512; got != want {
		t.Fatalf("unexpected balloon controller minActualMiB default: got %d want %d", got, want)
	}
	if got, want := manifest.QEMU.Devices.Balloon.Controller.MaxActualMiB, manifest.QEMU.Memory.SizeMiB; got != want {
		t.Fatalf("unexpected balloon controller maxActualMiB default: got %d want %d", got, want)
	}
	if got, want := manifest.QEMU.Devices.Balloon.Controller.GrowBelowAvailableMiB, 256; got != want {
		t.Fatalf("unexpected balloon controller grow threshold default: got %d want %d", got, want)
	}
	if got, want := manifest.QEMU.Devices.Balloon.Controller.ReclaimAboveAvailableMiB, 512; got != want {
		t.Fatalf("unexpected balloon controller reclaim threshold default: got %d want %d", got, want)
	}
	if got, want := manifest.QEMU.Devices.Balloon.Controller.StepMiB, 256; got != want {
		t.Fatalf("unexpected balloon controller step default: got %d want %d", got, want)
	}
	if got, want := manifest.QEMU.Devices.Balloon.Controller.PollIntervalSeconds, 5; got != want {
		t.Fatalf("unexpected balloon controller poll interval default: got %d want %d", got, want)
	}
	if got, want := manifest.QEMU.Devices.Balloon.Controller.ReclaimHoldoffSeconds, 30; got != want {
		t.Fatalf("unexpected balloon controller reclaim holdoff default: got %d want %d", got, want)
	}
}

func TestValidateRejectsInvalidBalloonControllerConfig(t *testing.T) {
	invalidBounds := validManifestWithBalloon()
	invalidBounds.QEMU.Devices.Balloon.Controller = &balloon.ControllerConfig{
		MinActualMiB: invalidBounds.QEMU.Memory.SizeMiB + 1,
	}
	if err := invalidBounds.Validate(); err == nil {
		t.Fatal("expected validation error for invalid balloon controller bounds")
	}

	invalidThresholds := validManifestWithBalloon()
	invalidThresholds.QEMU.Devices.Balloon.Controller = &balloon.ControllerConfig{
		GrowBelowAvailableMiB:    512,
		ReclaimAboveAvailableMiB: 512,
	}
	if err := invalidThresholds.Validate(); err == nil {
		t.Fatal("expected validation error for invalid balloon controller thresholds")
	}
}

func validManifestWithBalloon() *Manifest {
	manifest := validManifest()
	manifest.QEMU.Devices.Balloon = &balloon.Device{
		ID:        "balloon0",
		Transport: "pci",
	}
	return manifest
}

func validManifest() *Manifest {
	return &Manifest{
		Identity: Identity{HostName: "agent-sandbox"},
		Paths: Paths{
			WorkingDir: "/tmp/work",
			LockPath:   "/tmp/virtie.lock",
		},
		SSH: SSH{
			Argv: []string{"/bin/ssh"},
			User: "agent",
		},
		QEMU: QEMU{
			BinaryPath: "/bin/qemu-system-x86_64",
			Name:       "agent-sandbox",
			Machine: QEMUMachine{
				Type:    "microvm",
				Options: []string{"accel=kvm:tcg"},
			},
			CPU: QEMUCPU{
				Model: "host",
			},
			Memory: QEMUMemory{
				SizeMiB: 1024,
			},
			Kernel: QEMUKernel{
				Path:       "/tmp/vmlinuz",
				InitrdPath: "/tmp/initrd",
			},
			SMP: QEMUSMP{
				CPUs: 2,
			},
			QMP: QEMUQMP{
				SocketPath: "qmp.sock",
			},
			Devices: QEMUDevices{
				RNG: QEMURNGDevice{
					ID:        "rng0",
					Transport: "pci",
				},
				VirtioFS: []QEMUVirtioFSShare{
					{
						ID:         "fs0",
						SocketPath: "fs.sock",
						Tag:        "workspace",
						Transport:  "pci",
					},
				},
				Block: []QEMUBlockDevice{
					{
						ID:        "vda",
						ImagePath: "root.img",
						Transport: "pci",
					},
				},
				Network: []QEMUNetDevice{
					{
						ID:         "net0",
						Backend:    "user",
						MacAddress: "02:02:00:00:00:01",
						Transport:  "pci",
					},
				},
				VSOCK: QEMUVSOCKDevice{
					ID:        "vsock0",
					Transport: "pci",
				},
			},
		},
		Volumes: []Volume{
			{
				ImagePath:  "root.img",
				SizeMiB:    64,
				FSType:     "ext4",
				AutoCreate: true,
			},
		},
		VirtioFS: VirtioFS{Daemons: []VirtioFSDaemon{
			{
				Tag:        "workspace",
				SocketPath: "fs.sock",
				Command: Command{
					Path: "/tmp/virtiofsd-workspace",
				},
			},
		}},
	}
}
