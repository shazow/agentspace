package manifest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/adrg/xdg"
)

func TestLoadReadsFromReader(t *testing.T) {
	manifest := validManifest()
	manifest.QEMU.BinaryPath = "bin/qemu-system-x86_64"
	manifest.QEMU.Kernel.Path = "boot/vmlinuz"
	manifest.QEMU.Kernel.InitrdPath = "boot/initrd"
	manifest.QEMU.Devices.Block[0].ImagePath = "images/root.img"
	manifest.VirtioFS.Daemons[0].Command.Path = "bin/virtiofsd-workspace"

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	loaded, err := Load(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}

	qemu, err := loaded.ResolvedQEMU()
	if err != nil {
		t.Fatalf("resolve qemu: %v", err)
	}
	if got, want := qemu.BinaryPath, "/tmp/work/bin/qemu-system-x86_64"; got != want {
		t.Fatalf("unexpected qemu binary path: got %q want %q", got, want)
	}
	if got, want := qemu.Kernel.Path, "/tmp/work/boot/vmlinuz"; got != want {
		t.Fatalf("unexpected kernel path: got %q want %q", got, want)
	}
	if got, want := qemu.Kernel.InitrdPath, "/tmp/work/boot/initrd"; got != want {
		t.Fatalf("unexpected initrd path: got %q want %q", got, want)
	}
	if got, want := qemu.Devices.Block[0].ImagePath, "/tmp/work/images/root.img"; got != want {
		t.Fatalf("unexpected block image path: got %q want %q", got, want)
	}

	daemons, err := loaded.ResolvedVirtioFSDaemons()
	if err != nil {
		t.Fatalf("resolve virtiofs daemons: %v", err)
	}
	if got, want := daemons[0].Command.Path, "/tmp/work/bin/virtiofsd-workspace"; got != want {
		t.Fatalf("unexpected daemon command path: got %q want %q", got, want)
	}

	if got, want := loaded.ResolvedVolumes(), []Volume{
		{
			ImagePath:  "/tmp/work/root.img",
			SizeMiB:    64,
			FSType:     "ext4",
			AutoCreate: true,
		},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected resolved volumes: got %#v want %#v", got, want)
	}
}

func TestLoadRejectsTrailingData(t *testing.T) {
	data, err := json.Marshal(validManifest())
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	_, err = Load(strings.NewReader(string(data) + "\n{}"))
	if err == nil {
		t.Fatal("expected trailing data error")
	}
	if !strings.Contains(err.Error(), "unexpected trailing data") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestManifestResolvesSocketsFromRuntimeDir(t *testing.T) {
	runtimeDir := t.TempDir()
	setXDGTestRuntimeDir(t, runtimeDir)

	tests := []struct {
		name       string
		runtimeDir *string
		socketPath string
		wantSocket string
		wantQMP    string
		wantQGA    string
	}{
		{
			name:       "legacy working dir",
			runtimeDir: nil,
			socketPath: "fs.sock",
			wantSocket: "/tmp/work/fs.sock",
			wantQMP:    "/tmp/work/qmp.sock",
			wantQGA:    "/tmp/work/qga.sock",
		},
		{
			name:       "default runtime dir",
			runtimeDir: stringPtr(""),
			socketPath: "fs.sock",
			wantSocket: filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "fs.sock"),
			wantQMP:    filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "qmp.sock"),
			wantQGA:    filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "qga.sock"),
		},
		{
			name:       "relative runtime dir",
			runtimeDir: stringPtr("runtime"),
			socketPath: "fs.sock",
			wantSocket: "/tmp/work/runtime/fs.sock",
			wantQMP:    "/tmp/work/runtime/qmp.sock",
			wantQGA:    "/tmp/work/runtime/qga.sock",
		},
		{
			name:       "absolute runtime dir",
			runtimeDir: stringPtr("/tmp/runtime"),
			socketPath: "fs.sock",
			wantSocket: "/tmp/runtime/fs.sock",
			wantQMP:    "/tmp/runtime/qmp.sock",
			wantQGA:    "/tmp/runtime/qga.sock",
		},
		{
			name:       "absolute socket path bypasses runtime dir",
			runtimeDir: stringPtr(""),
			socketPath: "/tmp/explicit-fs.sock",
			wantSocket: "/tmp/explicit-fs.sock",
			wantQMP:    "/tmp/explicit-qmp.sock",
			wantQGA:    "/tmp/explicit-qga.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest()
			manifest.Paths.RuntimeDir = tt.runtimeDir
			manifest.VirtioFS.Daemons[0].SocketPath = tt.socketPath
			manifest.QEMU.Devices.VirtioFS[0].SocketPath = tt.socketPath
			manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
			if tt.name == "absolute socket path bypasses runtime dir" {
				manifest.QEMU.QMP.SocketPath = "/tmp/explicit-qmp.sock"
				manifest.QEMU.GuestAgent.SocketPath = "/tmp/explicit-qga.sock"
			}

			socketPaths, err := manifest.ResolvedSocketPaths()
			if err != nil {
				t.Fatalf("resolve socket paths: %v", err)
			}
			if got, want := socketPaths, []string{tt.wantSocket}; !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected socket paths: got %v want %v", got, want)
			}
			virtioFSSocketPaths, err := manifest.ResolvedVirtioFSSocketPaths()
			if err != nil {
				t.Fatalf("resolve virtiofs socket paths: %v", err)
			}
			if got, want := virtioFSSocketPaths, []string{tt.wantSocket}; !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected virtiofs socket paths: got %v want %v", got, want)
			}

			qmpSocketPath, err := manifest.ResolvedQMPSocketPath()
			if err != nil {
				t.Fatalf("resolve qmp socket path: %v", err)
			}
			if qmpSocketPath != tt.wantQMP {
				t.Fatalf("unexpected qmp socket path: got %q want %q", qmpSocketPath, tt.wantQMP)
			}
			guestAgentSocketPath, err := manifest.ResolvedGuestAgentSocketPath()
			if err != nil {
				t.Fatalf("resolve guest agent socket path: %v", err)
			}
			if guestAgentSocketPath != tt.wantQGA {
				t.Fatalf("unexpected guest agent socket path: got %q want %q", guestAgentSocketPath, tt.wantQGA)
			}

			qemu, err := manifest.ResolvedQEMU()
			if err != nil {
				t.Fatalf("resolve qemu config: %v", err)
			}
			if got, want := qemu.Devices.VirtioFS[0].SocketPath, tt.wantSocket; got != want {
				t.Fatalf("unexpected qemu virtiofs socket path: got %q want %q", got, want)
			}
			if got, want := qemu.QMP.SocketPath, tt.wantQMP; got != want {
				t.Fatalf("unexpected qemu qmp socket path: got %q want %q", got, want)
			}
			if got, want := qemu.GuestAgent.SocketPath, tt.wantQGA; got != want {
				t.Fatalf("unexpected qemu guest agent socket path: got %q want %q", got, want)
			}

			daemons, err := manifest.ResolvedVirtioFSDaemons()
			if err != nil {
				t.Fatalf("resolve virtiofs daemons: %v", err)
			}
			if got, want := daemons[0].SocketPath, tt.wantSocket; got != want {
				t.Fatalf("unexpected daemon socket path: got %q want %q", got, want)
			}
		})
	}
}

func TestManifestWriteFilesValidation(t *testing.T) {
	validContent := "aGVsbG8="
	validMode := "0640"

	tests := []struct {
		name      string
		configure func(*Manifest)
		wantError string
	}{
		{
			name: "valid content",
			configure: func(manifest *Manifest) {
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &validContent},
				}
			},
		},
		{
			name: "valid mode",
			configure: func(manifest *Manifest) {
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &validContent, Mode: &validMode},
				}
			},
		},
		{
			name: "allows arbitrary chown",
			configure: func(manifest *Manifest) {
				chown := ""
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &validContent, Chown: &chown},
				}
			},
		},
		{
			name: "valid host path",
			configure: func(manifest *Manifest) {
				hostPath := "agent.conf"
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Path: &hostPath},
				}
			},
		},
		{
			name: "requires guest agent socket",
			configure: func(manifest *Manifest) {
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &validContent},
				}
			},
			wantError: "manifest.qemu.guestAgent.socketPath is required",
		},
		{
			name: "rejects relative guest path",
			configure: func(manifest *Manifest) {
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"etc/agent.conf": {Content: &validContent},
				}
			},
			wantError: "guest path must be absolute",
		},
		{
			name: "rejects missing source",
			configure: func(manifest *Manifest) {
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {},
				}
			},
			wantError: "must set exactly one",
		},
		{
			name: "rejects duplicate source",
			configure: func(manifest *Manifest) {
				hostPath := "agent.conf"
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &validContent, Path: &hostPath},
				}
			},
			wantError: "must set exactly one",
		},
		{
			name: "rejects invalid base64",
			configure: func(manifest *Manifest) {
				invalidContent := "not base64"
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &invalidContent},
				}
			},
			wantError: "content must be valid base64",
		},
		{
			name: "rejects mode without leading zero",
			configure: func(manifest *Manifest) {
				mode := "640"
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &validContent, Mode: &mode},
				}
			},
			wantError: "mode must match",
		},
		{
			name: "rejects invalid octal mode",
			configure: func(manifest *Manifest) {
				mode := "0888"
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &validContent, Mode: &mode},
				}
			},
			wantError: "mode must match",
		},
		{
			name: "rejects symbolic mode",
			configure: func(manifest *Manifest) {
				mode := "u=rw"
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &validContent, Mode: &mode},
				}
			},
			wantError: "mode must match",
		},
		{
			name: "rejects empty mode",
			configure: func(manifest *Manifest) {
				mode := ""
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Content: &validContent, Mode: &mode},
				}
			},
			wantError: "mode must match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest()
			tt.configure(manifest)

			err := manifest.Validate()
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected validation error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestResolvedWriteFilesResolvesRelativeHostPaths(t *testing.T) {
	manifest := validManifest()
	content := "aGVsbG8="
	chown := "agent:users"
	mode := "0640"
	relativeHostPath := "files/agent.conf"
	absoluteHostPath := "/tmp/host.conf"
	manifest.WriteFiles = WriteFiles{
		"/etc/a.conf": {Path: &relativeHostPath},
		"/etc/b.conf": {Content: &content, Chown: &chown, Mode: &mode},
		"/etc/c.conf": {Path: &absoluteHostPath},
	}

	got := manifest.ResolvedWriteFiles()
	want := []ResolvedWriteFile{
		{GuestPath: "/etc/a.conf", HostPath: stringPtr("/tmp/work/files/agent.conf")},
		{GuestPath: "/etc/b.conf", Chown: &chown, Content: &content, Mode: &mode},
		{GuestPath: "/etc/c.conf", HostPath: &absoluteHostPath},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected resolved write files: got %#v want %#v", got, want)
	}
}

func TestManifestNotificationsValidationAndResolution(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		manifest := validManifest()
		if err := manifest.Validate(); err != nil {
			t.Fatalf("validate manifest: %v", err)
		}
		if manifest.Notifications.Command != nil {
			t.Fatalf("expected disabled notifications by default, got %#v", manifest.Notifications)
		}
	})

	t.Run("accepts command path args and states", func(t *testing.T) {
		manifest := validManifest()
		manifest.Notifications = Notifications{
			Command: &Command{
				Path: "bin/notify",
				Args: []string{"--verbose"},
			},
			States: []string{"runtime:resume", "balloon:resize"},
		}
		if err := manifest.Validate(); err != nil {
			t.Fatalf("validate manifest: %v", err)
		}

		resolved := manifest.ResolvedNotifications()
		if resolved.Command == nil {
			t.Fatal("expected resolved notification command")
		}
		if got, want := resolved.Command.Path, "/tmp/work/bin/notify"; got != want {
			t.Fatalf("unexpected resolved command path: got %q want %q", got, want)
		}
		if got, want := resolved.Command.Args, []string{"--verbose"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected resolved command args: got %v want %v", got, want)
		}
		if got, want := resolved.States, []string{"runtime:resume", "balloon:resize"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected resolved states: got %v want %v", got, want)
		}
	})

	t.Run("rejects args without path", func(t *testing.T) {
		data := []byte(`{
			"identity": {"hostName": "agent-sandbox"},
			"paths": {"workingDir": "/tmp/work", "lockPath": "/tmp/virtie.lock"},
			"ssh": {"argv": ["/bin/ssh"], "user": "agent"},
			"qemu": {
				"binaryPath": "/bin/qemu-system-x86_64",
				"name": "agent-sandbox",
				"machine": {"type": "microvm"},
				"cpu": {"model": "host"},
				"memory": {"sizeMiB": 1024},
				"kernel": {"path": "/tmp/vmlinuz", "initrdPath": "/tmp/initrd"},
				"smp": {"cpus": 2},
				"qmp": {"socketPath": "qmp.sock"},
				"devices": {
					"rng": {"id": "rng0", "transport": "pci"},
					"virtiofs": [{"id": "fs0", "socketPath": "fs.sock", "tag": "workspace", "transport": "pci"}],
					"block": [{"id": "vda", "imagePath": "root.img", "transport": "pci"}],
					"network": [{"id": "net0", "backend": "user", "macAddress": "02:02:00:00:00:01", "transport": "pci"}],
					"vsock": {"id": "vsock0", "transport": "pci"}
				}
			},
			"virtiofs": {"daemons": [{"tag": "workspace", "socketPath": "fs.sock", "command": {"path": "/tmp/virtiofsd-workspace"}}]},
			"notifications": {"command": {"args": ["--verbose"]}}
		}`)
		_, err := Load(bytes.NewReader(data))
		if err == nil || !strings.Contains(err.Error(), "manifest.notifications.command.path is required") {
			t.Fatalf("expected notification command path validation error, got %v", err)
		}
	})

	t.Run("preserves through load", func(t *testing.T) {
		manifest := validManifest()
		manifest.Notifications = Notifications{
			Command: &Command{
				Path: "/bin/notify",
				Args: []string{"--state"},
			},
			States: []string{"runtime:suspend"},
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}

		loaded, err := Load(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("load manifest: %v", err)
		}
		if loaded.Notifications.Command == nil {
			t.Fatal("expected notification command after load")
		}
		if got, want := *loaded.Notifications.Command, *manifest.Notifications.Command; !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected loaded command: got %#v want %#v", got, want)
		}
		if got, want := loaded.Notifications.States, manifest.Notifications.States; !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected loaded states: got %#v want %#v", got, want)
		}
	})
}

func TestManifestAllowsExternalVirtioFSSocket(t *testing.T) {
	manifest := validManifest()
	manifest.VirtioFS.Daemons = nil
	manifest.QEMU.Devices.VirtioFS[0].SocketPath = "/var/run/virtiofs-nix-store.sock"

	if err := manifest.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	managedSocketPaths, err := manifest.ResolvedSocketPaths()
	if err != nil {
		t.Fatalf("resolve managed socket paths: %v", err)
	}
	if len(managedSocketPaths) != 0 {
		t.Fatalf("unexpected managed socket paths: got %v want none", managedSocketPaths)
	}

	virtioFSSocketPaths, err := manifest.ResolvedVirtioFSSocketPaths()
	if err != nil {
		t.Fatalf("resolve virtiofs socket paths: %v", err)
	}
	if got, want := virtioFSSocketPaths, []string{"/var/run/virtiofs-nix-store.sock"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected virtiofs socket paths: got %v want %v", got, want)
	}
}

func stringPtr(value string) *string {
	return &value
}

func setXDGTestRuntimeDir(t *testing.T, runtimeDir string) {
	t.Helper()

	original, hadOriginal := os.LookupEnv("XDG_RUNTIME_DIR")
	if err := os.Setenv("XDG_RUNTIME_DIR", runtimeDir); err != nil {
		t.Fatalf("set XDG_RUNTIME_DIR: %v", err)
	}
	xdg.Reload()

	t.Cleanup(func() {
		var err error
		if hadOriginal {
			err = os.Setenv("XDG_RUNTIME_DIR", original)
		} else {
			err = os.Unsetenv("XDG_RUNTIME_DIR")
		}
		if err != nil {
			t.Fatalf("restore XDG_RUNTIME_DIR: %v", err)
		}
		xdg.Reload()
	})
}
