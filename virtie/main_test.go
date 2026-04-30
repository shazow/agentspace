package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParserRejectsInvalidCommandLines(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "launch missing manifest",
			args:    []string{"launch"},
			wantErr: "manifest",
		},
		{
			name:    "launch positional manifest",
			args:    []string{"launch", "/tmp/manifest.json"},
			wantErr: "manifest",
		},
		{
			name:    "suspend missing manifest",
			args:    []string{"suspend"},
			wantErr: "manifest",
		},
		{
			name:    "removed resume command",
			args:    []string{"resume", "--manifest=/tmp/manifest.json"},
			wantErr: "Unknown command",
		},
		{
			name:    "remote command without ssh",
			args:    []string{"launch", "--manifest=/tmp/manifest.json", "--", "echo", "hi"},
			wantErr: "remote command arguments require --ssh",
		},
		{
			name:    "invalid launch resume mode",
			args:    []string{"launch", "--resume=maybe", "--manifest=/tmp/manifest.json"},
			wantErr: "Invalid value",
		},
		{
			name:    "removed suspend exit flag",
			args:    []string{"suspend", "--exit", "--manifest=/tmp/manifest.json"},
			wantErr: "unknown flag `exit'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newParser().ParseArgs(tt.args)
			if err == nil {
				t.Fatalf("ParseArgs(%v) succeeded, expected error containing %q", tt.args, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ParseArgs(%v) error %q does not contain %q", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestParserAcceptsLaunchFlags(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		unwantedErrMsg string
	}{
		{
			name:           "resume no",
			args:           []string{"launch", "--resume=no", "--manifest=/tmp/manifest.json"},
			unwantedErrMsg: "Invalid value",
		},
		{
			name:           "resume auto",
			args:           []string{"launch", "--resume=auto", "--manifest=/tmp/manifest.json"},
			unwantedErrMsg: "Invalid value",
		},
		{
			name:           "resume force",
			args:           []string{"launch", "--resume=force", "--manifest=/tmp/manifest.json"},
			unwantedErrMsg: "Invalid value",
		},
		{
			name:           "ssh",
			args:           []string{"launch", "--ssh", "--manifest=/tmp/manifest.json"},
			unwantedErrMsg: "unknown flag `ssh'",
		},
		{
			name:           "verbose short",
			args:           []string{"launch", "-v", "--manifest=/tmp/manifest.json"},
			unwantedErrMsg: "unknown flag `v'",
		},
		{
			name:           "debug short",
			args:           []string{"launch", "-vv", "--manifest=/tmp/manifest.json"},
			unwantedErrMsg: "unknown flag `v'",
		},
		{
			name:           "verbose long",
			args:           []string{"launch", "--verbose", "--manifest=/tmp/manifest.json"},
			unwantedErrMsg: "unknown flag `verbose'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newParser().ParseArgs(tt.args)
			if err != nil && strings.Contains(err.Error(), tt.unwantedErrMsg) {
				t.Fatalf("ParseArgs(%v) rejected supported flag: %v", tt.args, err)
			}
		})
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
