package launch

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestFinalizeLockedPlanSetsCIDAndQEMUCommand(t *testing.T) {
	cfg := cidManifest(3, 5)
	wantCmd := exec.Command("/bin/qemu")
	plan := &Plan{Manifest: cfg}

	err := FinalizeLockedPlan(plan, cidCheckerFunc(func(cid int) (bool, error) {
		return cid == 4, nil
	}), func(gotManifest *manifest.Manifest, cid int, incoming bool) (*exec.Cmd, error) {
		if gotManifest != cfg {
			t.Fatalf("manifest: got %#v want %#v", gotManifest, cfg)
		}
		if cid != 4 {
			t.Fatalf("cid: got %d want 4", cid)
		}
		if incoming {
			t.Fatal("incoming should be false without resume state")
		}
		return wantCmd, nil
	})
	if err != nil {
		t.Fatalf("finalize plan: %v", err)
	}
	if plan.CID != 4 {
		t.Fatalf("plan cid: got %d want 4", plan.CID)
	}
	if plan.QEMUCommand != wantCmd {
		t.Fatalf("plan qemu command: got %#v want %#v", plan.QEMUCommand, wantCmd)
	}
}

func TestFinalizeLockedPlanBuildsIncomingCommandForResume(t *testing.T) {
	cfg := cidManifest(3, 5)
	plan := &Plan{Manifest: cfg, ResumeState: &SuspendState{CID: 3}}

	err := FinalizeLockedPlan(plan, nil, func(_ *manifest.Manifest, cid int, incoming bool) (*exec.Cmd, error) {
		if cid != 3 {
			t.Fatalf("cid: got %d want 3", cid)
		}
		if !incoming {
			t.Fatal("incoming should be true with resume state")
		}
		return exec.Command("/bin/qemu"), nil
	})
	if err != nil {
		t.Fatalf("finalize resume plan: %v", err)
	}
}

func TestFinalizeLockedPlanReturnsCIDError(t *testing.T) {
	wantErr := errors.New("cid failed")
	err := FinalizeLockedPlan(&Plan{Manifest: cidManifest(3, 5)}, cidCheckerFunc(func(int) (bool, error) {
		return false, wantErr
	}), func(*manifest.Manifest, int, bool) (*exec.Cmd, error) {
		t.Fatal("qemu builder should not run after CID failure")
		return nil, nil
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("cid error: got %v want %v", err, wantErr)
	}
}

func TestFinalizeLockedPlanReturnsQEMUBuilderError(t *testing.T) {
	wantErr := errors.New("qemu failed")
	err := FinalizeLockedPlan(&Plan{Manifest: cidManifest(3, 5)}, nil, func(*manifest.Manifest, int, bool) (*exec.Cmd, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("qemu error: got %v want %v", err, wantErr)
	}
}
