package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParserRequiresManifestFlag(t *testing.T) {
	tests := [][]string{
		{"launch"},
		{"launch", "/tmp/manifest.json"},
		{"suspend"},
	}

	for _, args := range tests {
		_, err := newParser().ParseArgs(args)
		if err == nil {
			t.Fatalf("ParseArgs(%v) succeeded, expected missing manifest error", args)
		}
		if !strings.Contains(err.Error(), "manifest") {
			t.Fatalf("ParseArgs(%v) error %q does not mention manifest", args, err)
		}
	}
}

func TestParserRejectsRemovedResumeCommand(t *testing.T) {
	_, err := newParser().ParseArgs([]string{"resume", "--manifest=/tmp/manifest.json"})
	if err == nil {
		t.Fatal("ParseArgs accepted removed resume command")
	}
	if !strings.Contains(err.Error(), "Unknown command") {
		t.Fatalf("unexpected error for removed resume command: %v", err)
	}
}

func TestParserAcceptsLaunchResumeModes(t *testing.T) {
	for _, mode := range []string{"no", "auto", "force"} {
		_, err := newParser().ParseArgs([]string{"launch", "--resume=" + mode, "--manifest=/tmp/manifest.json"})
		if err != nil && strings.Contains(err.Error(), "Invalid value") {
			t.Fatalf("ParseArgs rejected resume mode %q: %v", mode, err)
		}
	}
}

func TestParserRejectsInvalidLaunchResumeMode(t *testing.T) {
	_, err := newParser().ParseArgs([]string{"launch", "--resume=maybe", "--manifest=/tmp/manifest.json"})
	if err == nil {
		t.Fatal("ParseArgs accepted invalid resume mode")
	}
	if !strings.Contains(err.Error(), "Invalid value") {
		t.Fatalf("unexpected error for invalid resume mode: %v", err)
	}
}

func TestParserRejectsSuspendExitFlag(t *testing.T) {
	_, err := newParser().ParseArgs([]string{"suspend", "--exit", "--manifest=/tmp/manifest.json"})
	if err == nil {
		t.Fatal("ParseArgs accepted removed --exit flag")
	}
	if !strings.Contains(err.Error(), "unknown flag `exit'") {
		t.Fatalf("unexpected error for removed --exit flag: %v", err)
	}
}

func TestLoadLaunchManifestPersistsAbsoluteWorkingDir(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(testManifestJSON(".")), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	defer func() {
		if err := os.Chdir(oldDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	manifest, err := loadLaunchManifest("manifest.json")
	if err != nil {
		t.Fatalf("load launch manifest: %v", err)
	}
	if manifest.Paths.WorkingDir != tmpDir {
		t.Fatalf("unexpected working dir: got %q want %q", manifest.Paths.WorkingDir, tmpDir)
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read updated manifest: %v", err)
	}
	var document struct {
		Paths struct {
			WorkingDir string `json:"workingDir"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode updated manifest: %v", err)
	}
	if document.Paths.WorkingDir != tmpDir {
		t.Fatalf("unexpected persisted working dir: got %q want %q", document.Paths.WorkingDir, tmpDir)
	}
}

func testManifestJSON(workingDir string) string {
	return `{
  "identity": {
    "hostName": "test-vm"
  },
  "paths": {
    "workingDir": "` + workingDir + `",
    "lockPath": "virtie.lock"
  },
  "persistence": {
    "directories": ["state"],
    "baseDir": ".agentspace",
    "stateDir": ".agentspace"
  },
  "ssh": {
    "argv": ["/bin/ssh"],
    "user": "agent"
  },
  "qemu": {
    "binaryPath": "/bin/qemu-system-x86_64",
    "name": "test-vm",
    "machine": {
      "type": "microvm"
    },
    "cpu": {
      "model": "host"
    },
    "memory": {
      "sizeMiB": 256
    },
    "kernel": {
      "path": "/tmp/vmlinuz",
      "initrdPath": "/tmp/initrd"
    },
    "smp": {
      "cpus": 1
    },
    "qmp": {
      "socketPath": "qmp.sock"
    },
    "devices": {
      "rng": {
        "id": "rng0",
        "transport": "pci"
      },
      "virtiofs": [
        {
          "id": "fs0",
          "socketPath": "virtiofs.sock",
          "tag": "workspace",
          "transport": "pci"
        }
      ],
      "block": [
        {
          "id": "vda",
          "imagePath": "overlay.img",
          "transport": "pci"
        }
      ],
      "network": [
        {
          "id": "net0",
          "backend": "user",
          "macAddress": "02:02:00:00:00:01",
          "transport": "pci"
        }
      ],
      "vsock": {
        "id": "vsock0",
        "transport": "pci"
      }
    }
  },
  "virtiofs": {
    "daemons": [
      {
        "tag": "workspace",
        "socketPath": "virtiofs.sock",
        "command": {
          "path": "/bin/virtiofsd"
        }
      }
    ]
  }
}
`
}
