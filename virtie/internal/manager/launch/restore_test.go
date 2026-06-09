package launch

import (
	"context"
	"errors"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestRestoreRuntimeRestoresAndNotifies(t *testing.T) {
	notifier := &recordingNotifier{}
	plan := &Plan{
		Manifest: &manifest.Manifest{Identity: manifest.Identity{HostName: "agent"}},
		CID:      7,
		Notifier: notifier,
		ResumeState: &SuspendState{
			VMStatePath: "/tmp/agent.vmstate",
		},
	}
	var restorePath string
	if err := RestoreRuntime(context.Background(), RuntimeRestore{
		Plan: plan,
		Restore: func(_ context.Context, vmStatePath string) error {
			restorePath = vmStatePath
			return nil
		},
	}); err != nil {
		t.Fatalf("restore runtime: %v", err)
	}
	if restorePath != "/tmp/agent.vmstate" {
		t.Fatalf("restore path: got %q want /tmp/agent.vmstate", restorePath)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notification calls: got %d want 1", len(notifier.calls))
	}
	if notifier.calls[0].state != NotifyStateRuntimeResume {
		t.Fatalf("notification: %#v", notifier.calls[0])
	}
}

func TestRestoreRuntimeWrapsRestoreErrorWithoutNotification(t *testing.T) {
	restoreErr := errors.New("restore failed")
	wrappedErr := errors.New("wrapped restore")
	notifier := &recordingNotifier{}
	err := RestoreRuntime(context.Background(), RuntimeRestore{
		Plan: &Plan{
			Manifest:    &manifest.Manifest{},
			Notifier:    notifier,
			ResumeState: &SuspendState{VMStatePath: "/tmp/agent.vmstate"},
		},
		Restore: func(context.Context, string) error {
			return restoreErr
		},
		Wrap: func(err error) error {
			if !errors.Is(err, restoreErr) {
				t.Fatalf("restore err: got %v want %v", err, restoreErr)
			}
			return wrappedErr
		},
	})
	if !errors.Is(err, wrappedErr) {
		t.Fatalf("wrapped err: got %v want %v", err, wrappedErr)
	}
	if len(notifier.calls) != 0 {
		t.Fatalf("notification calls: got %d want 0", len(notifier.calls))
	}
}
