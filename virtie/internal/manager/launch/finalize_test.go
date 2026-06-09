package launch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

func TestSetupLockedPlanFinalizesAndPreparesFilesystem(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := cidManifest(3, 5)
	cfg.Persistence.BaseDir = filepath.Join(tmpDir, "state")
	cfg.Persistence.StateDir = filepath.Join(tmpDir, "state")
	cfg.Paths.RuntimeDir.Path = filepath.Join(tmpDir, "run")
	cfg.Paths.LockPath = filepath.Join(tmpDir, "lock")
	plan := &Plan{Manifest: cfg}
	cleanupCalled := false

	err := SetupLockedPlan(LockedPlanSetup{
		Plan:    plan,
		Checker: cidCheckerFunc(func(cid int) (bool, error) { return cid == 3, nil }),
		BuildQEMU: func(*manifest.Manifest, int, bool) (*exec.Cmd, error) {
			return exec.Command("/bin/qemu"), nil
		},
		Cleanup: func() error {
			cleanupCalled = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("setup locked plan: %v", err)
	}
	if plan.CID != 3 || plan.QEMUCommand == nil {
		t.Fatalf("plan not finalized: cid=%d qemu=%#v", plan.CID, plan.QEMUCommand)
	}
	if cleanupCalled {
		t.Fatal("cleanup should not run on successful setup")
	}
}

func TestSetupLockedPlanCleansUpAfterFinalizeError(t *testing.T) {
	wantErr := errors.New("finalize failed")
	cleanupCalled := false
	err := SetupLockedPlan(LockedPlanSetup{
		Plan:    &Plan{Manifest: cidManifest(3, 5)},
		Checker: cidCheckerFunc(func(int) (bool, error) { return false, wantErr }),
		BuildQEMU: func(*manifest.Manifest, int, bool) (*exec.Cmd, error) {
			t.Fatal("qemu builder should not run after cid error")
			return nil, nil
		},
		Cleanup: func() error {
			cleanupCalled = true
			return nil
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error: got %v want %v", err, wantErr)
	}
	if !cleanupCalled {
		t.Fatal("cleanup did not run after finalize error")
	}
}

func TestSetupLockedPlanCleansUpAfterFilesystemError(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := cidManifest(3, 5)
	cfg.Persistence.Directories = []string{filepath.Join(tmpDir, "state-file")}
	if err := os.WriteFile(cfg.Persistence.Directories[0], []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	plan := &Plan{Manifest: cfg}
	cleanupCalled := false
	err := SetupLockedPlan(LockedPlanSetup{
		Plan: plan,
		BuildQEMU: func(*manifest.Manifest, int, bool) (*exec.Cmd, error) {
			return exec.Command("/bin/qemu"), nil
		},
		Cleanup: func() error {
			cleanupCalled = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("expected filesystem setup error")
	}
	if !cleanupCalled {
		t.Fatal("cleanup did not run after filesystem error")
	}
}
