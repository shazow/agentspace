package launch

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestActivateRuntimeSequencesStartupAndProvision(t *testing.T) {
	lifecycle := NewLifecycle(nil, nil, nil)
	defer lifecycle.Stop()
	lifecycle.Suspend().Request()

	var events []string
	err := ActivateRuntime(context.Background(), RuntimeActivation{
		Lifecycle: lifecycle,
		MarkReady: func() {
			events = append(events, "ready")
		},
		Configure: func() {
			events = append(events, "configure")
		},
		StartControl: func(context.Context) error {
			events = append(events, "control")
			return nil
		},
		HandleSuspend: func(context.Context, *SuspendCoordinator) error {
			events = append(events, "queued-suspend")
			return nil
		},
		Provision: GuestProvision{
			Plan: &Plan{
				Manifest: &manifest.Manifest{},
				Paths:    RuntimePaths{SSHReadySocket: "/tmp/ssh-ready.sock"},
			},
			WriteFiles: func(context.Context) error {
				events = append(events, "write-files")
				return nil
			},
			WaitSSHReady: func(context.Context, string) error {
				events = append(events, "wait-ssh")
				return nil
			},
		},
		EnableWriteBack: func() {
			events = append(events, "enable-writeback")
		},
	})
	if err != nil {
		t.Fatalf("activate runtime: %v", err)
	}
	if got, want := events, []string{"ready", "configure", "control", "queued-suspend", "write-files", "wait-ssh", "enable-writeback"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %v want %v", got, want)
	}
}

func TestActivateRuntimeWrapsControlStartupError(t *testing.T) {
	controlErr := errors.New("control failed")
	wrappedErr := errors.New("wrapped control")
	var provisioned bool

	err := ActivateRuntime(context.Background(), RuntimeActivation{
		StartControl: func(context.Context) error {
			return controlErr
		},
		WrapControl: func(err error) error {
			if !errors.Is(err, controlErr) {
				t.Fatalf("control err: got %v want %v", err, controlErr)
			}
			return wrappedErr
		},
		Provision: GuestProvision{
			Plan: &Plan{Manifest: &manifest.Manifest{}},
			WriteFiles: func(context.Context) error {
				provisioned = true
				return nil
			},
		},
	})
	if !errors.Is(err, wrappedErr) {
		t.Fatalf("wrapped err: got %v want %v", err, wrappedErr)
	}
	if provisioned {
		t.Fatal("provision should not run after control startup failure")
	}
}
