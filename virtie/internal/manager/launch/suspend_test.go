package launch

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestSaveRuntimeSuspendSavesMetadataAndNotifies(t *testing.T) {
	cfg := testManifest(t)
	notifier := &recordingNotifier{}
	staleState := VMStatePath(cfg)
	if err := os.MkdirAll(cfg.Persistence.StateDir, 0o755); err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	if err := os.WriteFile(staleState, []byte("stale"), 0o644); err != nil {
		t.Fatalf("write stale vm state: %v", err)
	}

	var savePath string
	if err := SaveRuntimeSuspend(context.Background(), RuntimeSuspendSave{
		Manifest:      cfg,
		QMPSocketPath: "/tmp/qmp.sock",
		CID:           7,
		Notifier:      notifier,
		Save: func(_ context.Context, vmStatePath string) error {
			savePath = vmStatePath
			if _, err := os.Stat(vmStatePath); !os.IsNotExist(err) {
				t.Fatalf("stale vm state still exists before save: %v", err)
			}
			return os.WriteFile(vmStatePath, []byte("saved"), 0o644)
		},
	}); err != nil {
		t.Fatalf("save runtime suspend: %v", err)
	}
	if savePath != VMStatePath(cfg) {
		t.Fatalf("save path: got %q want %q", savePath, VMStatePath(cfg))
	}
	state, err := ReadSuspendState(cfg)
	if err != nil {
		t.Fatalf("read suspend state: %v", err)
	}
	if state.HostName != cfg.Identity.HostName ||
		state.QMPSocketPath != "/tmp/qmp.sock" ||
		state.VMStatePath != VMStatePath(cfg) ||
		state.CID != 7 ||
		state.Status != "saved" {
		t.Fatalf("state: %#v", state)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("notification calls: got %d want 1", len(notifier.calls))
	}
	call := notifier.calls[0]
	if call.state != NotifyStateRuntimeSuspend ||
		call.values["host_name"] != cfg.Identity.HostName ||
		call.values["qmp_socket_path"] != "/tmp/qmp.sock" ||
		call.values["vm_state_path"] != VMStatePath(cfg) ||
		call.values["cid"] != "7" {
		t.Fatalf("notification: %#v", call)
	}
}

func TestSaveRuntimeSuspendWrapsSaveError(t *testing.T) {
	saveErr := errors.New("save failed")
	wrappedErr := errors.New("wrapped save")
	err := SaveRuntimeSuspend(context.Background(), RuntimeSuspendSave{
		Manifest: testManifest(t),
		Save: func(context.Context, string) error {
			return saveErr
		},
		Wrap: func(err error) error {
			if !errors.Is(err, saveErr) {
				t.Fatalf("save err: got %v want %v", err, saveErr)
			}
			return wrappedErr
		},
	})
	if !errors.Is(err, wrappedErr) {
		t.Fatalf("wrapped err: got %v want %v", err, wrappedErr)
	}
}
