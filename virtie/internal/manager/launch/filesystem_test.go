package launch

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveStaleSocketPathsDeletesStateDirSocketWithoutPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	socketPath := filepath.Join(stateDir, "qmp.sock")
	listener := createUnixSocket(t, socketPath)
	defer listener.Close()

	err := RemoveStaleSocketPaths(SocketCleanupOptions{
		Paths:    []string{socketPath},
		StateDir: stateDir,
		Prompt: func(string) (bool, error) {
			t.Fatalf("prompt should not run for stale sockets under state dir")
			return false, nil
		},
	})
	if err != nil {
		t.Fatalf("remove stale socket: %v", err)
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected trusted stale socket to be removed, stat err = %v", err)
	}
}

func TestRemoveStaleSocketPathsPromptsForSocketOutsideStateDir(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	socketPath := filepath.Join(tmpDir, "outside.sock")
	listener := createUnixSocket(t, socketPath)
	defer listener.Close()

	var prompted string
	err := RemoveStaleSocketPaths(SocketCleanupOptions{
		Paths:    []string{socketPath},
		StateDir: stateDir,
		Prompt: func(path string) (bool, error) {
			prompted = path
			return false, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "stale socket") {
		t.Fatalf("expected stale socket prompt refusal error, got %v", err)
	}
	if prompted != socketPath {
		t.Fatalf("prompted path: got %q want %q", prompted, socketPath)
	}
	if _, err := os.Lstat(socketPath); err != nil {
		t.Fatalf("declined stale socket should remain: %v", err)
	}
}

func TestRemoveStaleSocketPathsAlwaysDeleteSkipsPrompt(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	socketPath := filepath.Join(tmpDir, "outside.sock")
	listener := createUnixSocket(t, socketPath)
	defer listener.Close()

	err := RemoveStaleSocketPaths(SocketCleanupOptions{
		Paths:        []string{socketPath},
		StateDir:     stateDir,
		AlwaysDelete: true,
		Prompt: func(string) (bool, error) {
			t.Fatalf("prompt should not run when always delete is enabled")
			return false, nil
		},
	})
	if err != nil {
		t.Fatalf("remove stale socket: %v", err)
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale socket to be removed, stat err = %v", err)
	}
}

func TestRemoveStaleSocketPathsRejectsNonSocket(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	path := filepath.Join(stateDir, "not-a-socket")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	err := RemoveStaleSocketPaths(SocketCleanupOptions{
		Paths:        []string{path},
		StateDir:     stateDir,
		AlwaysDelete: true,
		Prompt: func(string) (bool, error) {
			t.Fatalf("prompt should not run for non-socket paths")
			return false, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("expected non-socket rejection, got %v", err)
	}
	if _, statErr := os.Lstat(path); statErr != nil {
		t.Fatalf("non-socket path should remain: %v", statErr)
	}
}

func createUnixSocket(t *testing.T, path string) net.Listener {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create socket parent: %v", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}
	return listener
}
