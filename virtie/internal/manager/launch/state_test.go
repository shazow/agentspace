package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestSuspendStateRoundTrip(t *testing.T) {
	cfg := testManifest(t)
	state := SuspendState{
		QMPSocketPath: "/run/qmp.sock",
		VMStatePath:   VMStatePath(cfg),
		CID:           7,
		Status:        "saved",
	}

	if err := WriteSuspendStateData(cfg, state); err != nil {
		t.Fatalf("write suspend state: %v", err)
	}
	readState, err := ReadSuspendState(cfg)
	if err != nil {
		t.Fatalf("read suspend state: %v", err)
	}
	if readState.HostName != cfg.Identity.HostName || readState.Status != "saved" || readState.CID != 7 || readState.Timestamp.IsZero() {
		t.Fatalf("unexpected suspend state: %#v", readState)
	}
	saved, err := HasSavedSuspendState(cfg)
	if err != nil {
		t.Fatalf("has saved suspend state: %v", err)
	}
	if !saved {
		t.Fatal("expected saved suspend state")
	}
	if err := RemoveSuspendState(cfg); err != nil {
		t.Fatalf("remove suspend state: %v", err)
	}
	if _, err := os.Stat(SuspendStatePath(cfg)); !os.IsNotExist(err) {
		t.Fatalf("expected suspend state removal, got %v", err)
	}
}

func TestLaunchPIDRoundTripAndLockValidation(t *testing.T) {
	cfg := testManifest(t)
	if err := WriteLaunchPID(cfg, 12345); err != nil {
		t.Fatalf("write launch pid: %v", err)
	}
	pid, err := ReadLaunchPID(cfg)
	if err != nil {
		t.Fatalf("read launch pid: %v", err)
	}
	if pid != 12345 {
		t.Fatalf("unexpected launch pid: got %d want 12345", pid)
	}

	lockPath := cfg.ResolvedLockPath()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("create lock dir: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("12345\n"), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := ValidateLaunchLock(cfg, 12345); err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("expected stale lock error, got %v", err)
	}

	if err := RemoveLaunchPID(cfg, 12345); err != nil {
		t.Fatalf("remove launch pid: %v", err)
	}
	if _, err := os.Stat(LaunchPIDPath(cfg)); !os.IsNotExist(err) {
		t.Fatalf("expected launch pid removal, got %v", err)
	}
}

func testManifest(t *testing.T) *manifest.Manifest {
	t.Helper()

	tmpDir := t.TempDir()
	return &manifest.Manifest{
		Identity: manifest.Identity{HostName: "agent"},
		Paths: manifest.Paths{
			WorkingDir: tmpDir,
			LockPath:   filepath.Join(tmpDir, ".agentspace", "agent.lock"),
		},
		Persistence: manifest.Persistence{
			StateDir: filepath.Join(tmpDir, ".agentspace"),
		},
	}
}
