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
			name:    "remote command without ssh",
			args:    []string{"launch", "--manifest=/tmp/manifest.json", "--", "echo", "hi"},
			wantErr: "remote command arguments require --ssh",
		},
		{
			name:    "invalid launch resume mode",
			args:    []string{"launch", "--resume=maybe", "--manifest=/tmp/manifest.json"},
			wantErr: "Invalid value",
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
		WorkingDir string `json:"working_dir"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode updated manifest: %v", err)
	}
	if document.WorkingDir != tmpDir {
		t.Fatalf("unexpected persisted working dir: got %q want %q", document.WorkingDir, tmpDir)
	}
}

func TestLoadLaunchManifestPersistsPassthroughWorkspaceMountPoint(t *testing.T) {
	tmpDir := t.TempDir()
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	data := strings.Replace(testManifestJSON("."), `  "state_dir": ".agentspace",
`, `  "state_dir": ".agentspace",
  "workspace": {
    "mode": "passthrough"
  },
`, 1)
	if err := os.WriteFile(manifestPath, []byte(data), 0o644); err != nil {
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
	if manifest.Workspace.MountPoint != tmpDir {
		t.Fatalf("unexpected in-memory workspace mount point: got %q want %q", manifest.Workspace.MountPoint, tmpDir)
	}

	updated, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read updated manifest: %v", err)
	}
	var document struct {
		WorkingDir string `json:"working_dir"`
		Workspace  struct {
			Mode       string  `json:"mode"`
			MountPoint *string `json:"mount_point"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(updated, &document); err != nil {
		t.Fatalf("decode updated manifest: %v", err)
	}
	if document.WorkingDir != tmpDir {
		t.Fatalf("unexpected persisted working dir: got %q want %q", document.WorkingDir, tmpDir)
	}
	if document.Workspace.Mode != "passthrough" {
		t.Fatalf("unexpected workspace mode: got %q want %q", document.Workspace.Mode, "passthrough")
	}
	if document.Workspace.MountPoint == nil || *document.Workspace.MountPoint != tmpDir {
		var got string
		if document.Workspace.MountPoint != nil {
			got = *document.Workspace.MountPoint
		}
		t.Fatalf("unexpected workspace mount point: got %q want %q", got, tmpDir)
	}
}

func testManifestJSON(workingDir string) string {
	return `{
  "host_name": "test-vm",
  "working_dir": "` + workingDir + `",
  "state_dir": ".agentspace",
  "ssh": {
    "exec": ["/bin/ssh"],
    "user": "agent"
  },
  "qemu": {
    "exec": ["/bin/qemu-system-x86_64"]
  },
  "machine": {
    "type": "microvm",
    "memory": 256,
    "vcpu": 1
  },
  "kernel": {
    "path": "/tmp/vmlinuz",
    "initrd_path": "/tmp/initrd"
  },
  "mounts": [
    {
      "type": "virtiofs",
      "tag": "workspace",
      "virtiofsd_socket": "virtiofs.sock",
      "virtiofsd_exec": ["/bin/virtiofsd"]
    }
  ],
  "volumes": [
    {
      "image": "overlay.img"
    }
  ]
}
`
}
