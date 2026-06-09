package launch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestGuestFilePayloadRejectsHostSymlinkWhenFollowLinksFalse(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "target")
	if err := os.WriteFile(targetPath, []byte("target content"), 0o644); err != nil {
		t.Fatalf("write target fixture: %v", err)
	}
	linkPath := filepath.Join(tmpDir, "link")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("create symlink fixture: %v", err)
	}

	_, err := GuestFilePayloadBase64(manifest.ResolvedWriteFile{
		GuestPath:   "/etc/from-link",
		Content:     manifest.WriteFileContent{Kind: manifest.WriteFileContentPath, Path: linkPath},
		FollowLinks: false,
	})
	if err == nil || !strings.Contains(err.Error(), "followLinks is false") {
		t.Fatalf("expected followLinks symlink error, got %v", err)
	}

	payload, err := GuestFilePayloadBase64(manifest.ResolvedWriteFile{
		GuestPath:   "/etc/from-link",
		Content:     manifest.WriteFileContent{Kind: manifest.WriteFileContentPath, Path: linkPath},
		FollowLinks: true,
	})
	if err != nil {
		t.Fatalf("expected followLinks=true to read symlink target: %v", err)
	}
	if got, want := payload, "dGFyZ2V0IGNvbnRlbnQ="; got != want {
		t.Fatalf("unexpected symlink target payload: got %q want %q", got, want)
	}
}

func TestWriteBackHostPathFollowsHostSymlinkWhenEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "target-file")
	if err := os.WriteFile(targetPath, []byte("original"), 0o644); err != nil {
		t.Fatalf("write target fixture: %v", err)
	}
	linkPath := filepath.Join(tmpDir, "host-link")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Fatalf("create symlink fixture: %v", err)
	}

	got, err := WriteBackHostPath(manifest.ResolvedWriteFile{
		GuestPath:   "/var/lib/virtie/host",
		Content:     manifest.WriteFileContent{Kind: manifest.WriteFileContentPath, Path: linkPath},
		FollowLinks: true,
	})
	if err != nil {
		t.Fatalf("write-back host path: %v", err)
	}
	if got != targetPath {
		t.Fatalf("host path: got %q want %q", got, targetPath)
	}
}

func TestWriteHostFileAtomicPreservesExistingMode(t *testing.T) {
	tmpDir := t.TempDir()
	hostPath := filepath.Join(tmpDir, "file")
	if err := os.WriteFile(hostPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := WriteHostFileAtomic(hostPath, []byte("new")); err != nil {
		t.Fatalf("write atomic: %v", err)
	}
	data, err := os.ReadFile(hostPath)
	if err != nil {
		t.Fatalf("read host path: %v", err)
	}
	if got, want := string(data), "new"; got != want {
		t.Fatalf("data: got %q want %q", got, want)
	}
	info, err := os.Stat(hostPath)
	if err != nil {
		t.Fatalf("stat host path: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("mode: got %s want %s", got, want)
	}
}
