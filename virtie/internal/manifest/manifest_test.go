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
	document := validDocument()
	document.QEMU.Exec = []string{"bin/qemu-system-x86_64"}
	document.Kernel.Path = "boot/vmlinuz"
	document.Kernel.InitrdPath = "boot/initrd"
	document.Mounts = append(document.Mounts, MountFacts{
		Type:          "9p",
		SourcePath:    "shares/cache",
		Tag:           "cache",
		SecurityModel: "none",
	})
	document.Volumes[0].ImagePath = "images/root.img"
	document.Mounts[0].VirtioFSDExec = []string{"bin/virtiofsd-workspace"}

	data, err := json.Marshal(document)
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
			ImagePath:  "/tmp/work/images/root.img",
			SizeMiB:    256,
			FSType:     "ext4",
			AutoCreate: true,
		},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected resolved volumes: got %#v want %#v", got, want)
	}
}

func TestLoadRejectsTrailingData(t *testing.T) {
	data, err := json.Marshal(validDocument())
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

func TestDocumentWriteFilesFollowLinksLowersToManifest(t *testing.T) {
	followLinks := false
	writeBack := true
	document := validDocument()
	document.WriteFiles = []WriteFileFacts{
		{
			GuestPath:   "/etc/source.conf",
			Path:        stringPtr("source.conf"),
			FollowLinks: &followLinks,
			WriteBack:   &writeBack,
		},
	}

	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	loaded, err := Load(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}

	files := loaded.ResolvedWriteFiles()
	if len(files) != 1 {
		t.Fatalf("expected one write file, got %#v", files)
	}
	if got := files[0].FollowLinks; got != false {
		t.Fatalf("unexpected followLinks default: got %v want false", got)
	}
	if got := files[0].WriteBack; got != true {
		t.Fatalf("unexpected writeBack value: got %v want true", got)
	}
}

func TestLoadTOMLExamples(t *testing.T) {
	for _, path := range []string{
		"../../examples/manifest-simple.toml",
		"../../examples/manifest-full.toml",
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			if _, err := LoadBytes(data, path); err != nil {
				t.Fatalf("load example: %v", err)
			}
		})
	}
}

func TestUpdateWorkingDirBytesPersistsPassthroughWorkspaceMountPoint(t *testing.T) {
	document := validDocument()
	document.Workspace = WorkspaceFacts{Mode: "passthrough", MountPoint: "/home/agent/workspace"}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	updated, err := UpdateWorkingDirBytes(data, "manifest.json", "/tmp/work")
	if err != nil {
		t.Fatalf("update manifest working dir: %v", err)
	}

	loaded, err := Load(bytes.NewReader(updated))
	if err != nil {
		t.Fatalf("load updated manifest: %v", err)
	}
	if got, want := loaded.Workspace.Mode, "passthrough"; got != want {
		t.Fatalf("unexpected workspace mode: got %q want %q", got, want)
	}
	if got, want := loaded.Workspace.MountPoint, "/tmp/work"; got != want {
		t.Fatalf("unexpected passthrough mount point: got %q want %q", got, want)
	}
}

func TestManifestSSHRetryDelayDefaultsAndValidation(t *testing.T) {
	manifest := validManifest()
	if manifest.SSH.RetryDelay != nil {
		t.Fatalf("test fixture should leave retry delay unset before validation, got %g", *manifest.SSH.RetryDelay)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("validate manifest: %v", err)
	}
	if got, want := manifest.SSHRetryDelay(25*time.Millisecond), 500*time.Millisecond; got != want {
		t.Fatalf("unexpected default ssh retry delay: got %s want %s", got, want)
	}
	if got, want := manifest.QEMU.SSHReady.SocketPath, ""; got != want {
		t.Fatalf("unexpected default ssh readiness socket: got %q want %q", got, want)
	}
	emptySSH := validManifest()
	emptySSH.SSH.Argv = nil
	if err := emptySSH.Validate(); err != nil {
		t.Fatalf("validate manifest with omitted ssh argv: %v", err)
	}
	if len(emptySSH.SSH.Argv) != 0 {
		t.Fatalf("expected omitted ssh argv to remain empty, got %#v", emptySSH.SSH.Argv)
	}
	if !manifest.QEMU.NoGraphicEnabled() {
		t.Fatalf("expected manifest without graphics to default to noGraphic")
	}

	custom := validManifest()
	custom.SSH.RetryDelay = float64Ptr(0.25)
	if err := custom.Validate(); err != nil {
		t.Fatalf("validate custom retry delay: %v", err)
	}
	if got, want := custom.SSHRetryDelay(time.Second), 250*time.Millisecond; got != want {
		t.Fatalf("unexpected custom ssh retry delay: got %s want %s", got, want)
	}

	invalid := validManifest()
	invalid.SSH.RetryDelay = float64Ptr(-1)
	err := invalid.Validate()
	if err == nil || !strings.Contains(err.Error(), "manifest.ssh.retryDelay must be a finite number greater than or equal to zero") {
		t.Fatalf("expected retry delay validation error, got %v", err)
	}
}

func TestDocumentSSHReadySocketDefaultAndEnable(t *testing.T) {
	omitted := validDocument()
	omitted.SSH.ReadySocket = ""
	omittedManifest, err := omitted.Manifest()
	if err != nil {
		t.Fatalf("lower manifest with omitted readiness socket: %v", err)
	}
	if got, want := omittedManifest.QEMU.SSHReady.SocketPath, ""; got != want {
		t.Fatalf("unexpected default ssh readiness socket: got %q want %q", got, want)
	}

	enabled := validDocument()
	enabled.SSH.ReadySocket = "ssh-ready.sock"
	enabledManifest, err := enabled.Manifest()
	if err != nil {
		t.Fatalf("lower manifest with enabled readiness socket: %v", err)
	}
	if got, want := enabledManifest.QEMU.SSHReady.SocketPath, "ssh-ready.sock"; got != want {
		t.Fatalf("unexpected enabled ssh readiness socket: got %q want %q", got, want)
	}
	sshReadySocketPath, err := omittedManifest.ResolvedSSHReadySocketPath()
	if err != nil {
		t.Fatalf("resolve default ssh readiness socket: %v", err)
	}
	if sshReadySocketPath != "" {
		t.Fatalf("expected default ssh readiness socket to resolve to empty path, got %q", sshReadySocketPath)
	}
}

func TestDocumentSSHExecDefaultsToEmpty(t *testing.T) {
	document := validDocument()
	document.SSH.Exec = nil

	manifest, err := document.Manifest()
	if err != nil {
		t.Fatalf("lower manifest with omitted ssh exec: %v", err)
	}
	if len(manifest.SSH.Argv) != 0 {
		t.Fatalf("expected omitted ssh exec to lower to empty argv, got %#v", manifest.SSH.Argv)
	}
}

func TestDocumentSSHAutoprovisionLowersToManifest(t *testing.T) {
	document := validDocument()
	document.SSH.Autoprovision = true

	manifest, err := document.Manifest()
	if err != nil {
		t.Fatalf("lower manifest with ssh autoprovision: %v", err)
	}
	if !manifest.SSH.Autoprovision {
		t.Fatal("expected ssh autoprovision to lower to manifest")
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
			if got, want := daemons[0].SourcePath, "/tmp/work/shares/workspace"; got != want {
				t.Fatalf("unexpected daemon source path: got %q want %q", got, want)
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
			name: "valid write back host path",
			configure: func(manifest *Manifest) {
				hostPath := "agent.conf"
				writeBack := true
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Path: &hostPath, WriteBack: &writeBack},
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
			name: "rejects write back without host path",
			configure: func(manifest *Manifest) {
				writeBack := true
				manifest.QEMU.GuestAgent.SocketPath = "qga.sock"
				manifest.WriteFiles = WriteFiles{
					"/etc/agent.conf": {Text: &validText, WriteBack: &writeBack},
				}
			},
			wantError: "writeBack requires path",
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
	followLinksFalse := false
	writeBackTrue := true
	relativeHostPath := "files/agent.conf"
	absoluteHostPath := "/tmp/host.conf"
	manifest.WriteFiles = WriteFiles{
		"/etc/a.conf": {Path: &relativeHostPath, Overwrite: &overwrite, FollowLinks: &followLinksFalse, WriteBack: &writeBackTrue},
		"/etc/b.conf": {Text: &text, Chown: &chown, Mode: &mode, Overwrite: &overwriteTrue},
		"/etc/c.conf": {Path: &absoluteHostPath},
	}

	got := manifest.ResolvedWriteFiles()
	want := []ResolvedWriteFile{
		{GuestPath: "/etc/a.conf", HostPath: stringPtr("/tmp/work/files/agent.conf"), Overwrite: false, FollowLinks: false, WriteBack: true},
		{GuestPath: "/etc/b.conf", Chown: &chown, Text: &text, Mode: &mode, Overwrite: true, FollowLinks: true},
		{GuestPath: "/etc/c.conf", HostPath: &absoluteHostPath, Overwrite: false, FollowLinks: true},
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
			"host_name": "agent-sandbox",
			"working_dir": "/tmp/work",
			"state_dir": ".virtie",
			"ssh": {"exec": ["/bin/ssh"], "user": "agent"},
			"qemu": {"exec": ["/bin/qemu-system-x86_64"]},
			"machine": {"memory": 1024},
			"kernel": {"path": "/tmp/vmlinuz", "initrd_path": "/tmp/initrd"},
			"mounts": [{"type": "virtiofs", "tag": "workspace", "virtiofsd_socket": "fs.sock", "virtiofsd_exec": ["/tmp/virtiofsd-workspace"]}],
			"volumes": [{"image": "root.img", "size": 256, "create": true}],
			"notifications": {"exec": ["--verbose"]}
		}`)
		loaded, err := Load(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("load manifest: %v", err)
		}
		if loaded.Notifications.Command == nil {
			t.Fatal("expected notification command after load")
		}
		if got := loaded.Notifications.Command.Path; got != "--verbose" {
			t.Fatalf("unexpected notification command path: got %q want --verbose", got)
		}
	})

	t.Run("preserves through load", func(t *testing.T) {
		document := validDocument()
		document.Notifications = NotificationsFacts{
			Exec:   []string{"/bin/notify", "--state"},
			States: []string{"runtime:suspend"},
		}
		data, err := json.Marshal(document)
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
		if got, want := *loaded.Notifications.Command, (Command{Path: "/bin/notify", Args: []string{"--state"}}); !reflect.DeepEqual(got, want) {
			t.Fatalf("unexpected loaded command: got %#v want %#v", got, want)
		}
		if got, want := loaded.Notifications.States, document.Notifications.States; !reflect.DeepEqual(got, want) {
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

	externalSocketPaths, err := manifest.ResolvedExternalVirtioFSSocketPaths()
	if err != nil {
		t.Fatalf("resolve external virtiofs socket paths: %v", err)
	}
	if got, want := externalSocketPaths, []string{"/var/run/virtiofs-nix-store.sock"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected external virtiofs socket paths: got %v want %v", got, want)
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
			manifest.QEMU.Knobs.NoGraphic = boolPtr(false)
			manifest.QEMU.Graphics = &QEMUGraphics{Backend: backend}

			if err := manifest.Validate(); err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}

	t.Run("rejects unsupported backend", func(t *testing.T) {
		manifest := validManifest()
		manifest.QEMU.Knobs.NoGraphic = boolPtr(false)
		manifest.QEMU.Graphics = &QEMUGraphics{Backend: "vnc"}

		err := manifest.Validate()
		if err == nil || !strings.Contains(err.Error(), "manifest.qemu.graphics.backend must be one of gtk or cocoa") {
			t.Fatalf("expected graphics backend validation error, got %v", err)
		}
	})
}

func TestLoadRejectsMalformedForwardEndpoints(t *testing.T) {
	tests := []struct {
		name      string
		host      string
		guest     string
		wantError string
	}{
		{
			name:      "host missing port",
			host:      "127.0.0.1",
			guest:     "10.0.2.15:22",
			wantError: "manifest.networks[0].forward[0].host missing :port",
		},
		{
			name:      "host non integer port",
			host:      "127.0.0.1:http",
			guest:     "10.0.2.15:22",
			wantError: "manifest.networks[0].forward[0].host port must be an integer",
		},
		{
			name:      "guest out of range port",
			host:      "127.0.0.1:2222",
			guest:     "10.0.2.15:70000",
			wantError: "manifest.networks[0].forward[0].guest port must be between 1 and 65535",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := validDocument()
			document.Networks = []NetworkFacts{
				{
					Forward: []ForwardPort{
						{
							Proto: "tcp",
							From:  "host",
							Host:  tt.host,
							Guest: tt.guest,
						},
					},
				},
			}

			data, err := json.Marshal(document)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}

			_, err = Load(bytes.NewReader(data))
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestLoadDefaultsForwardPortProtoAndFrom(t *testing.T) {
	document := validDocument()
	document.Networks = []NetworkFacts{
		{
			Forward: []ForwardPort{
				{
					Host:  "127.0.0.1:2222",
					Guest: "10.0.2.15:22",
				},
			},
		},
	}

	data, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	loaded, err := Load(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if got, want := loaded.QEMU.Devices.Network[0].NetdevOptions, []string{"hostfwd=tcp:127.0.0.1:2222-10.0.2.15:22"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected network forward options: got %#v want %#v", got, want)
	}
}

func TestLoadGuestForwardUsesTunnelExecTemplate(t *testing.T) {
	tests := []struct {
		name          string
		fwdTunnelExec []string
		want          []string
	}{
		{
			name: "default netcat",
			want: []string{"guestfwd=tcp:10.0.2.15:2222-cmd:nc 127.0.0.1 22"},
		},
		{
			name:          "explicit netcat",
			fwdTunnelExec: []string{"/bin/nc", "$HOST", "$PORT"},
			want:          []string{"guestfwd=tcp:10.0.2.15:2222-cmd:/bin/nc 127.0.0.1 22"},
		},
		{
			name:          "socat",
			fwdTunnelExec: []string{"socat", "-", "TCP:$HOST:$PORT"},
			want:          []string{"guestfwd=tcp:10.0.2.15:2222-cmd:socat - TCP:127.0.0.1:22"},
		},
		{
			name:          "quotes shell-sensitive args",
			fwdTunnelExec: []string{"~/bin/nc", "$HOST", "$PORT"},
			want:          []string{"guestfwd=tcp:10.0.2.15:2222-cmd:\\~/bin/nc 127.0.0.1 22"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := validDocument()
			document.QEMU.FwdTunnelExec = tt.fwdTunnelExec
			document.Networks = []NetworkFacts{
				{
					Forward: []ForwardPort{
						{
							Proto: "tcp",
							From:  "guest",
							Host:  "127.0.0.1:22",
							Guest: "10.0.2.15:2222",
						},
					},
				},
			}

			data, err := json.Marshal(document)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}

			loaded, err := Load(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("load manifest: %v", err)
			}
			if got := loaded.QEMU.Devices.Network[0].NetdevOptions; !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("unexpected network forward options: got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidForwardOptions(t *testing.T) {
	tests := []struct {
		name      string
		port      ForwardPort
		wantError string
	}{
		{
			name: "unsupported proto",
			port: ForwardPort{
				Proto: "icmp",
				From:  "host",
				Host:  "127.0.0.1:2222",
				Guest: "10.0.2.15:22",
			},
			wantError: "manifest.networks[0].forward[0].proto must be one of tcp or udp",
		},
		{
			name: "unsupported direction",
			port: ForwardPort{
				Proto: "tcp",
				From:  "outside",
				Host:  "127.0.0.1:2222",
				Guest: "10.0.2.15:22",
			},
			wantError: "manifest.networks[0].forward[0].from must be one of host or guest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := validDocument()
			document.Networks = []NetworkFacts{
				{
					Forward: []ForwardPort{tt.port},
				},
			}

			data, err := json.Marshal(document)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}

			_, err = Load(bytes.NewReader(data))
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error containing %q, got %v", tt.wantError, err)
			}
		})
	}
}

func TestLoadTreatsHeadlessGraphicsAsAbsentForTransport(t *testing.T) {
	for _, graphics := range []*GraphicsFacts{
		nil,
		{Backend: "headless"},
	} {
		name := "omitted"
		if graphics != nil {
			name = "explicit headless"
		}
		t.Run(name, func(t *testing.T) {
			document := validDocument()
			document.Mounts = nil
			document.Graphics = graphics

			data, err := json.Marshal(document)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}

			loaded, err := Load(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("load manifest: %v", err)
			}
			if got, want := loaded.QEMU.Devices.RNG.Transport, "mmio"; got != want {
				t.Fatalf("unexpected rng transport: got %q want %q", got, want)
			}
			if got, want := loaded.QEMU.Devices.Network[0].Transport, "mmio"; got != want {
				t.Fatalf("unexpected network transport: got %q want %q", got, want)
			}
			if loaded.QEMU.Graphics != nil {
				t.Fatalf("expected no qemu graphics for headless manifest, got %#v", loaded.QEMU.Graphics)
			}
		})
	}
}

func TestManifestNoGraphicDefaultsPreserveExplicitFalse(t *testing.T) {
	t.Run("defaults omitted headless manifest to noGraphic", func(t *testing.T) {
		manifest := validManifest()
		manifest.QEMU.Knobs.NoGraphic = nil

		if err := manifest.Validate(); err != nil {
			t.Fatalf("validate manifest: %v", err)
		}
		if !manifest.QEMU.NoGraphicEnabled() {
			t.Fatalf("expected omitted noGraphic without graphics to default true")
		}
	})

	t.Run("preserves explicit false without typed graphics", func(t *testing.T) {
		document := validDocument()
		document.Graphics = &GraphicsFacts{Backend: "gtk"}

		data, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}

		loaded, err := Load(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("load manifest: %v", err)
		}
		if loaded.QEMU.Knobs.NoGraphic == nil || *loaded.QEMU.Knobs.NoGraphic {
			t.Fatalf("expected explicit noGraphic=false to be preserved, got %#v", loaded.QEMU.Knobs.NoGraphic)
		}
	})

	t.Run("defaults typed graphics to graphical", func(t *testing.T) {
		manifest := validManifest()
		manifest.QEMU.Knobs.NoGraphic = nil
		manifest.QEMU.Graphics = &QEMUGraphics{Backend: "gtk"}

		if err := manifest.Validate(); err != nil {
			t.Fatalf("validate manifest: %v", err)
		}
		if manifest.QEMU.Knobs.NoGraphic == nil || *manifest.QEMU.Knobs.NoGraphic {
			t.Fatalf("expected omitted noGraphic with graphics to default false, got %#v", manifest.QEMU.Knobs.NoGraphic)
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

func validDocument() Document {
	return Document{
		HostName:   "agent-sandbox",
		WorkingDir: "/tmp/work",
		StateDir:   ".virtie",
		QEMU: QEMUFacts{
			Exec:           []string{"/bin/qemu-system-x86_64"},
			Seccomp:        true,
			MachineOptions: map[string]string{"accel": "kvm:tcg"},
		},
		Machine: MachineFacts{
			Type:   "microvm",
			VCPU:   intPtr(2),
			CPU:    "host",
			Memory: 1024,
		},
		Kernel: KernelFacts{
			Path:       "/tmp/vmlinuz",
			InitrdPath: "/tmp/initrd",
		},
		Mounts: []MountFacts{
			{
				Type:          "virtiofs",
				Tag:           "workspace",
				SourcePath:    "shares/workspace",
				SocketPath:    "fs.sock",
				VirtioFSDExec: []string{"/tmp/virtiofsd-workspace"},
			},
		},
		Volumes: []VolumeFacts{
			{
				ImagePath:  "root.img",
				SizeMiB:    256,
				FSType:     "ext4",
				AutoCreate: true,
			},
		},
		Networks: []NetworkFacts{
			{
				ID:   "net0",
				Type: "user",
				MAC:  "02:02:00:00:00:01",
			},
		},
		SSH: SSHFacts{
			Exec: []string{"/bin/ssh"},
			User: "agent",
		},
	}
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
