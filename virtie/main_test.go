package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOptionsDeclaresCommands(t *testing.T) {
	tests := []struct {
		field           string
		command         string
		description     string
		longDescription string
	}{
		{
			field:           "Launch",
			command:         "launch",
			description:     "Launch a virtiofs + ssh sandbox session",
			longDescription: "Start configured host-side run processes, launch QEMU directly, then optionally attach over ssh.",
		},
		{
			field:           "Suspend",
			command:         "suspend",
			description:     "Suspend a running sandbox session",
			longDescription: "Save QEMU state to disk and exit the launch session.",
		},
		{
			field:           "Hotplug",
			command:         "hotplug",
			description:     "Attach or detach a predefined hotplug device",
			longDescription: "Attach or detach a device described under manifest [hotplug].",
		},
	}

	optionsType := reflect.TypeOf(Options{})
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			field, ok := optionsType.FieldByName(tt.field)
			if !ok {
				t.Fatalf("Options is missing %s command field", tt.field)
			}
			if field.Type.Kind() != reflect.Struct || field.Type.Name() != "" {
				t.Fatalf("%s field type = %s, want anonymous struct in Options", tt.field, field.Type)
			}
			if got := field.Tag.Get("command"); got != tt.command {
				t.Fatalf("%s command tag = %q, want %q", tt.field, got, tt.command)
			}
			if got := field.Tag.Get("description"); got != tt.description {
				t.Fatalf("%s description tag = %q, want %q", tt.field, got, tt.description)
			}
			if got := field.Tag.Get("long-description"); got != tt.longDescription {
				t.Fatalf("%s long-description tag = %q, want %q", tt.field, got, tt.longDescription)
			}
		})
	}
}

func TestParserRejectsInvalidCommandLines(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "launch missing manifest",
			args:    []string{"launch"},
			wantErr: "no manifest path provided",
		},
		{
			name:    "launch positional manifest",
			args:    []string{"launch", "/tmp/manifest.json"},
			wantErr: "remote command arguments require --ssh",
		},
		{
			name:    "suspend missing manifest",
			args:    []string{"suspend"},
			wantErr: "no manifest path provided",
		},
		{
			name:    "remote command without ssh",
			args:    []string{"--manifest=/tmp/manifest.json", "launch", "--", "echo", "hi"},
			wantErr: "remote command arguments require --ssh",
		},
		{
			name:    "invalid launch resume mode",
			args:    []string{"--manifest=/tmp/manifest.json", "launch", "--resume=maybe"},
			wantErr: "Invalid value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args)
			if err == nil {
				t.Fatalf("run(%v) succeeded, expected error containing %q", tt.args, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("run(%v) error %q does not contain %q", tt.args, err, tt.wantErr)
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
			args:           []string{"--manifest=/tmp/manifest.json", "launch", "--resume=no"},
			unwantedErrMsg: "Invalid value",
		},
		{
			name:           "resume auto",
			args:           []string{"--manifest=/tmp/manifest.json", "launch", "--resume=auto"},
			unwantedErrMsg: "Invalid value",
		},
		{
			name:           "resume force",
			args:           []string{"--manifest=/tmp/manifest.json", "launch", "--resume=force"},
			unwantedErrMsg: "Invalid value",
		},
		{
			name:           "ssh",
			args:           []string{"--manifest=/tmp/manifest.json", "launch", "--ssh"},
			unwantedErrMsg: "unknown flag `ssh'",
		},
		{
			name:           "verbose short",
			args:           []string{"--manifest=/tmp/manifest.json", "launch", "-v"},
			unwantedErrMsg: "unknown flag `v'",
		},
		{
			name:           "debug short",
			args:           []string{"--manifest=/tmp/manifest.json", "launch", "-vv"},
			unwantedErrMsg: "unknown flag `v'",
		},
		{
			name:           "verbose long",
			args:           []string{"--manifest=/tmp/manifest.json", "launch", "--verbose"},
			unwantedErrMsg: "unknown flag `verbose'",
		},
		{
			name:           "always delete sockets",
			args:           []string{"--manifest=/tmp/manifest.json", "launch", "--always-delete-sockets"},
			unwantedErrMsg: "unknown flag `always-delete-sockets'",
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

func TestParserAcceptsSharedOptionsBeforeOrAfterSubcommand(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.toml")
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "manifest root first",
			args: []string{"--manifest=" + manifestPath, "launch"},
		},
		{
			name: "manifest command after",
			args: []string{"launch", "--manifest=" + manifestPath},
		},
		{
			name: "verbose root first",
			args: []string{"--manifest=" + manifestPath, "-vv", "launch"},
		},
		{
			name: "verbose command after",
			args: []string{"launch", "-vv", "--manifest=" + manifestPath},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args)
			if err == nil {
				t.Fatalf("run(%v) succeeded, expected manifest load error", tt.args)
			}
			if strings.Contains(err.Error(), "unknown flag `manifest'") {
				t.Fatalf("run(%v) rejected supported manifest placement: %v", tt.args, err)
			}
			if strings.Contains(err.Error(), "unknown flag `v'") {
				t.Fatalf("run(%v) rejected supported verbose placement: %v", tt.args, err)
			}
			if !strings.Contains(err.Error(), `open manifest`) || !strings.Contains(err.Error(), manifestPath) {
				t.Fatalf("run(%v) did not parse manifest path, got %v", tt.args, err)
			}
		})
	}
}

func TestResolveManifestPathDefaultsToTOMLThenJSON(t *testing.T) {
	tmpDir := t.TempDir()
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

	jsonPath := filepath.Join(tmpDir, "manifest.json")
	if err := os.WriteFile(jsonPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write json manifest: %v", err)
	}
	resolved, err := resolveManifestPath("")
	if err != nil {
		t.Fatalf("resolve json default: %v", err)
	}
	if resolved != jsonPath {
		t.Fatalf("unexpected json default: got %q want %q", resolved, jsonPath)
	}

	tomlPath := filepath.Join(tmpDir, "manifest.toml")
	if err := os.WriteFile(tomlPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write toml manifest: %v", err)
	}
	resolved, err = resolveManifestPath("")
	if err != nil {
		t.Fatalf("resolve toml default: %v", err)
	}
	if resolved != tomlPath {
		t.Fatalf("unexpected toml default: got %q want %q", resolved, tomlPath)
	}
}

func TestResolveManifestPathRequiresExplicitOrDefaultManifest(t *testing.T) {
	tmpDir := t.TempDir()
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

	_, err = resolveManifestPath("")
	if err == nil || !strings.Contains(err.Error(), "no manifest path provided") {
		t.Fatalf("expected missing manifest error, got %v", err)
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

	manifest, err := loadLaunchManifest("manifest.json", slog.New(slog.DiscardHandler))
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
      "virtiofs": {
        "socket": "virtiofs.sock",
        "bin": "/bin/virtiofsd"
      }
    },
    {
      "type": "image",
      "source": "overlay.img"
    }
  ]
}
`
}
