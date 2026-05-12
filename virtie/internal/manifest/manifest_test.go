package manifest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
)

func TestLoadReadsFromReader(t *testing.T) {
	manifest := validManifest()
	manifest.QEMU.BinaryPath = "bin/qemu-system-x86_64"
	manifest.QEMU.Kernel.Path = "boot/vmlinuz"
	manifest.QEMU.Kernel.InitrdPath = "boot/initrd"
	manifest.QEMU.Devices.NineP = []QEMUNinePShare{
		{
			ID:            "fs9p0",
			SourcePath:    "shares/cache",
			Tag:           "cache",
			SecurityModel: "none",
			Transport:     "pci",
		},
	}
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
	if got, want := qemu.Devices.NineP[0].SourcePath, "/tmp/work/shares/cache"; got != want {
		t.Fatalf("unexpected 9p source path: got %q want %q", got, want)
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
			SizeMiB:    256,
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

func TestLoadRejectsLegacyWriteFileContent(t *testing.T) {
	manifest := validManifest()
	manifest.QEMU.GuestAgent.SocketPath = "qga.sock"

	var data map[string]any
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := json.Unmarshal(encoded, &data); err != nil {
		t.Fatalf("unmarshal manifest map: %v", err)
	}
	data["writeFiles"] = map[string]any{
		"/etc/agent.conf": map[string]any{"content": "aGVsbG8="},
	}

	encoded, err = json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal legacy manifest: %v", err)
	}

	_, err = Load(bytes.NewReader(encoded))
	if err == nil || !strings.Contains(err.Error(), "must set exactly one of text or path") {
		t.Fatalf("expected legacy content validation error, got %v", err)
	}
}

func TestManifestSSHRetryDelayDefaultsAndValidation(t *testing.T) {
	manifest := validManifest()
	if manifest.SSH.RetryDelayMS != nil {
		t.Fatalf("test fixture should leave retry delay unset before validation, got %d", *manifest.SSH.RetryDelayMS)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("validate manifest: %v", err)
	}
	if got, want := manifest.SSHRetryDelay(25*time.Millisecond), time.Second; got != want {
		t.Fatalf("unexpected default ssh retry delay: got %s want %s", got, want)
	}
	if got, want := manifest.QEMU.SSHReady.SocketPath, "ssh-ready.sock"; got != want {
		t.Fatalf("unexpected default ssh readiness socket: got %q want %q", got, want)
	}
	if !manifest.QEMU.Knobs.NoGraphic {
		t.Fatalf("expected manifest without graphics to default to noGraphic")
	}

	custom := validManifest()
	custom.SSH.RetryDelayMS = intPtr(250)
	if err := custom.Validate(); err != nil {
		t.Fatalf("validate custom retry delay: %v", err)
	}
	if got, want := custom.SSHRetryDelay(time.Second), 250*time.Millisecond; got != want {
		t.Fatalf("unexpected custom ssh retry delay: got %s want %s", got, want)
	}

	invalid := validManifest()
	invalid.SSH.RetryDelayMS = intPtr(-1)
	err := invalid.Validate()
	if err == nil || !strings.Contains(err.Error(), "manifest.ssh.retryDelayMs must be greater than or equal to zero") {
		t.Fatalf("expected retry delay validation error, got %v", err)
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
		wantReady  string
	}{
		{
			name:       "legacy working dir",
			runtimeDir: nil,
			socketPath: "fs.sock",
			wantSocket: "/tmp/work/fs.sock",
			wantQMP:    "/tmp/work/qmp.sock",
			wantQGA:    "/tmp/work/qga.sock",
			wantReady:  "/tmp/work/ssh-ready.sock",
		},
		{
			name:       "default runtime dir",
			runtimeDir: stringPtr(""),
			socketPath: "fs.sock",
			wantSocket: filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "fs.sock"),
			wantQMP:    filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "qmp.sock"),
			wantQGA:    filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "qga.sock"),
			wantReady:  filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "ssh-ready.sock"),
		},
		{
			name:       "relative runtime dir",
			runtimeDir: stringPtr("runtime"),
			socketPath: "fs.sock",
			wantSocket: "/tmp/work/runtime/fs.sock",
			wantQMP:    "/tmp/work/runtime/qmp.sock",
			wantQGA:    "/tmp/work/runtime/qga.sock",
			wantReady:  "/tmp/work/runtime/ssh-ready.sock",
		},
		{
			name:       "absolute runtime dir",
			runtimeDir: stringPtr("/tmp/runtime"),
			socketPath: "fs.sock",
			wantSocket: "/tmp/runtime/fs.sock",
			wantQMP:    "/tmp/runtime/qmp.sock",
			wantQGA:    "/tmp/runtime/qga.sock",
			wantReady:  "/tmp/runtime/ssh-ready.sock",
		},
		{
			name:       "absolute socket path bypasses runtime dir",
			runtimeDir: stringPtr(""),
			socketPath: "/tmp/explicit-fs.sock",
			wantSocket: "/tmp/explicit-fs.sock",
			wantQMP:    "/tmp/explicit-qmp.sock",
			wantQGA:    "/tmp/explicit-qga.sock",
			wantReady:  "/tmp/explicit-ready.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest()
			manifest.Paths.RuntimeDir = tt.runtimeDir
			manifest.VirtioFS.Daemons[0].SocketPath = tt.socketPath
			manifest.QEMU.Devices.VirtioFS[0].SocketPath = tt.socketPath
			manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
			manifest.QEMU.SSHReady.SocketPath = "ssh-ready.sock"
			if tt.name == "absolute socket path bypasses runtime dir" {
				manifest.QEMU.QMP.SocketPath = "/tmp/explicit-qmp.sock"
				manifest.QEMU.GuestAgent.SocketPath = "/tmp/explicit-qga.sock"
				manifest.QEMU.SSHReady.SocketPath = "/tmp/explicit-ready.sock"
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
			sshReadySocketPath, err := manifest.ResolvedSSHReadySocketPath()
			if err != nil {
				t.Fatalf("resolve ssh readiness socket path: %v", err)
			}
			if sshReadySocketPath != tt.wantReady {
				t.Fatalf("unexpected ssh readiness socket path: got %q want %q", sshReadySocketPath, tt.wantReady)
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
			if got, want := qemu.SSHReady.SocketPath, tt.wantReady; got != want {
				t.Fatalf("unexpected qemu ssh readiness socket path: got %q want %q", got, want)
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
	validText := "hello"
	validMode := "0640"

	tests := []struct {
		name      string
		configure func(*Manifest)
		wantError string
	}{
		{
			name: "valid text",
			configure: func(manifest *Manifest) {
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Text: &validText},
				}
			},
		},
		{
			name: "valid mode",
			configure: func(manifest *Manifest) {
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Text: &validText, Mode: &validMode},
				}
			},
		},
		{
			name: "allows arbitrary chown",
			configure: func(manifest *Manifest) {
				chown := ""
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Text: &validText, Chown: &chown},
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
					"/etc/agent.conf": {Text: &validText},
				}
			},
			wantError: "manifest.qemu.guestAgent.socketPath is required",
		},
		{
			name: "rejects relative guest path",
			configure: func(manifest *Manifest) {
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"etc/agent.conf": {Text: &validText},
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
					"/etc/agent.conf": {Text: &validText, Path: &hostPath},
				}
			},
			wantError: "must set exactly one",
		},
		{
			name: "rejects empty host path",
			configure: func(manifest *Manifest) {
				hostPath := ""
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Path: &hostPath},
				}
			},
			wantError: "path must not be empty",
		},
		{
			name: "allows mode without leading zero",
			configure: func(manifest *Manifest) {
				mode := "640"
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Text: &validText, Mode: &mode},
				}
			},
		},
		{
			name: "rejects invalid octal mode",
			configure: func(manifest *Manifest) {
				mode := "0888"
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Text: &validText, Mode: &mode},
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
					"/etc/agent.conf": {Text: &validText, Mode: &mode},
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
					"/etc/agent.conf": {Text: &validText, Mode: &mode},
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
	text := "hello"
	chown := "agent:users"
	mode := "0640"
	overwrite := false
	overwriteTrue := true
	relativeHostPath := "files/agent.conf"
	absoluteHostPath := "/tmp/host.conf"
	manifest.WriteFiles = WriteFiles{
		"/etc/a.conf": {Path: &relativeHostPath, Overwrite: &overwrite},
		"/etc/b.conf": {Text: &text, Chown: &chown, Mode: &mode, Overwrite: &overwriteTrue},
		"/etc/c.conf": {Path: &absoluteHostPath},
	}

	got := manifest.ResolvedWriteFiles()
	want := []ResolvedWriteFile{
		{GuestPath: "/etc/a.conf", HostPath: stringPtr("/tmp/work/files/agent.conf"), Overwrite: false},
		{GuestPath: "/etc/b.conf", Chown: &chown, Text: &text, Mode: &mode, Overwrite: true},
		{GuestPath: "/etc/c.conf", HostPath: &absoluteHostPath, Overwrite: false},
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

	t.Run("accepts args without path", func(t *testing.T) {
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
		loaded, err := Load(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("load manifest: %v", err)
		}
		if loaded.Notifications.Command == nil {
			t.Fatal("expected notification command after load")
		}
		if got := loaded.Notifications.Command.Path; got != "" {
			t.Fatalf("unexpected notification command path: got %q want empty", got)
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

func TestManifestValidatesQEMUDeviceTransports(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Manifest)
		wantError string
	}{
		{
			name: "rng",
			configure: func(manifest *Manifest) {
				manifest.QEMU.Devices.RNG.Transport = "usb"
			},
			wantError: "manifest.qemu.devices.rng.transport must be one of pci, mmio, or ccw",
		},
		{
			name: "vsock",
			configure: func(manifest *Manifest) {
				manifest.QEMU.Devices.VSOCK.Transport = "usb"
			},
			wantError: "manifest.qemu.devices.vsock.transport must be one of pci, mmio, or ccw",
		},
		{
			name: "virtiofs",
			configure: func(manifest *Manifest) {
				manifest.QEMU.Devices.VirtioFS[0].Transport = "usb"
			},
			wantError: "manifest.qemu.devices.virtiofs[0].transport must be one of pci, mmio, or ccw",
		},
		{
			name: "virtiofs socket path",
			configure: func(manifest *Manifest) {
				manifest.QEMU.Devices.VirtioFS[0].SocketPath = ""
			},
			wantError: "manifest.qemu.devices.virtiofs[0].socketPath is required",
		},
		{
			name: "balloon",
			configure: func(manifest *Manifest) {
				manifest.QEMU.Devices.Balloon = validManifestWithBalloon().QEMU.Devices.Balloon
				manifest.QEMU.Devices.Balloon.Transport = "usb"
			},
			wantError: "manifest.qemu.devices.balloon.transport must be one of pci, mmio, or ccw",
		},
		{
			name: "9p",
			configure: func(manifest *Manifest) {
				manifest.QEMU.Devices.NineP = []QEMUNinePShare{{Transport: "usb"}}
			},
			wantError: "manifest.qemu.devices.9p[0].transport must be one of pci, mmio, or ccw",
		},
		{
			name: "block",
			configure: func(manifest *Manifest) {
				manifest.QEMU.Devices.Block[0].Transport = "usb"
			},
			wantError: "manifest.qemu.devices.block[0].transport must be one of pci, mmio, or ccw",
		},
		{
			name: "network",
			configure: func(manifest *Manifest) {
				manifest.QEMU.Devices.Network[0].Transport = "usb"
			},
			wantError: "manifest.qemu.devices.network[0].transport must be one of pci, mmio, or ccw",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest()
			tt.configure(manifest)

			err := manifest.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected validation error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestManifestValidatesQEMUGraphicsBackend(t *testing.T) {
	for _, backend := range []string{"gtk", "cocoa"} {
		t.Run("allows "+backend, func(t *testing.T) {
			manifest := validManifest()
			manifest.QEMU.Knobs.NoGraphic = false
			manifest.QEMU.Graphics = &QEMUGraphics{Backend: backend}

			if err := manifest.Validate(); err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}

	t.Run("rejects unsupported backend", func(t *testing.T) {
		manifest := validManifest()
		manifest.QEMU.Knobs.NoGraphic = false
		manifest.QEMU.Graphics = &QEMUGraphics{Backend: "vnc"}

		err := manifest.Validate()
		if err == nil || !strings.Contains(err.Error(), "manifest.qemu.graphics.backend must be one of gtk or cocoa") {
			t.Fatalf("expected graphics backend validation error, got %v", err)
		}
	})
}

func TestManifestAllowsInitrdApplianceWithoutStorageDevices(t *testing.T) {
	manifest := validManifest()
	manifest.QEMU.Devices.VirtioFS = nil
	manifest.QEMU.Devices.Block = nil
	manifest.QEMU.Devices.Network = nil
	manifest.Volumes = nil
	manifest.VirtioFS.Daemons = nil

	if err := manifest.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	virtioFSSocketPaths, err := manifest.ResolvedVirtioFSSocketPaths()
	if err != nil {
		t.Fatalf("resolve virtiofs socket paths: %v", err)
	}
	if len(virtioFSSocketPaths) != 0 {
		t.Fatalf("unexpected virtiofs socket paths: got %v want none", virtioFSSocketPaths)
	}
	if volumes := manifest.ResolvedVolumes(); len(volumes) != 0 {
		t.Fatalf("unexpected volumes: got %v want none", volumes)
	}
}

func TestManifestVolumeValidation(t *testing.T) {
	t.Run("allows empty image path when not auto creating", func(t *testing.T) {
		manifest := validManifest()
		manifest.Volumes = []Volume{{ImagePath: "", AutoCreate: false}}

		if err := manifest.Validate(); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
	})

	t.Run("requires image path when auto creating", func(t *testing.T) {
		manifest := validManifest()
		manifest.Volumes = []Volume{{ImagePath: "", SizeMiB: 256, AutoCreate: true}}

		err := manifest.Validate()
		if err == nil || !strings.Contains(err.Error(), "manifest.volumes[0].imagePath is required") {
			t.Fatalf("expected auto-create image path validation error, got %v", err)
		}
	})

	t.Run("requires size when auto creating", func(t *testing.T) {
		manifest := validManifest()
		manifest.Volumes = []Volume{{ImagePath: "root.img", SizeMiB: 0, AutoCreate: true}}

		err := manifest.Validate()
		if err == nil || !strings.Contains(err.Error(), "manifest.volumes[0].sizeMiB must be greater than zero") {
			t.Fatalf("expected auto-create size validation error, got %v", err)
		}
	})

	t.Run("rejects auto-created volumes below minimum size", func(t *testing.T) {
		manifest := validManifest()
		manifest.Volumes = []Volume{{ImagePath: "root.img", SizeMiB: 255, AutoCreate: true}}

		err := manifest.Validate()
		if err == nil || !strings.Contains(err.Error(), "manifest.volumes[0].sizeMiB must be at least 256") {
			t.Fatalf("expected auto-create minimum size validation error, got %v", err)
		}
	})

	t.Run("rejects non-ext4 filesystem when auto creating", func(t *testing.T) {
		manifest := validManifest()
		manifest.Volumes = []Volume{{ImagePath: "root.img", SizeMiB: 256, FSType: "xfs", AutoCreate: true}}

		err := manifest.Validate()
		if err == nil || !strings.Contains(err.Error(), `manifest.volumes[0].fsType must be "ext4"`) {
			t.Fatalf("expected auto-create fsType validation error, got %v", err)
		}
	})

	t.Run("rejects mkfs extra args when auto creating", func(t *testing.T) {
		manifest := validManifest()
		manifest.Volumes = []Volume{{
			ImagePath:     "root.img",
			SizeMiB:       256,
			AutoCreate:    true,
			MkfsExtraArgs: []string{"-E", "discard"},
		}}

		err := manifest.Validate()
		if err == nil || !strings.Contains(err.Error(), "manifest.volumes[0].mkfsExtraArgs is not supported") {
			t.Fatalf("expected auto-create mkfsExtraArgs validation error, got %v", err)
		}
	})

	t.Run("allows label when auto creating ext4", func(t *testing.T) {
		manifest := validManifest()
		label := "persist"
		manifest.Volumes = []Volume{{ImagePath: "root.img", SizeMiB: 256, AutoCreate: true, Label: &label}}

		if err := manifest.Validate(); err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
	})
}

func TestManifestAllowsRuntimeAndQEMUPassedCPUs(t *testing.T) {
	for _, cpus := range []*int{nil, intPtr(0), intPtr(-1)} {
		manifest := validManifest()
		manifest.QEMU.SMP.CPUs = cpus

		if err := manifest.Validate(); err != nil {
			t.Fatalf("unexpected validation error for cpus=%v: %v", cpus, err)
		}
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
