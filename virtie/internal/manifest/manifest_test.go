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
	}{
		{
			name:       "legacy working dir",
			runtimeDir: nil,
			socketPath: "fs.sock",
			wantSocket: "/tmp/work/fs.sock",
			wantQMP:    "/tmp/work/qmp.sock",
		},
		{
			name:       "default runtime dir",
			runtimeDir: stringPtr(""),
			socketPath: "fs.sock",
			wantSocket: filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "fs.sock"),
			wantQMP:    filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "qmp.sock"),
		},
		{
			name:       "relative runtime dir",
			runtimeDir: stringPtr("runtime"),
			socketPath: "fs.sock",
			wantSocket: "/tmp/work/runtime/fs.sock",
			wantQMP:    "/tmp/work/runtime/qmp.sock",
		},
		{
			name:       "absolute runtime dir",
			runtimeDir: stringPtr("/tmp/runtime"),
			socketPath: "fs.sock",
			wantSocket: "/tmp/runtime/fs.sock",
			wantQMP:    "/tmp/runtime/qmp.sock",
		},
		{
			name:       "absolute socket path bypasses runtime dir",
			runtimeDir: stringPtr(""),
			socketPath: "/tmp/explicit-fs.sock",
			wantSocket: "/tmp/explicit-fs.sock",
			wantQMP:    "/tmp/explicit-qmp.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest()
			manifest.Paths.RuntimeDir = tt.runtimeDir
			manifest.VirtioFS.Daemons[0].SocketPath = tt.socketPath
			manifest.QEMU.Devices.VirtioFS[0].SocketPath = tt.socketPath
			if tt.name == "absolute socket path bypasses runtime dir" {
				manifest.QEMU.QMP.SocketPath = "/tmp/explicit-qmp.sock"
			}

			socketPaths, err := manifest.ResolvedSocketPaths()
			if err != nil {
				t.Fatalf("resolve socket paths: %v", err)
			}
			if got, want := socketPaths, []string{tt.wantSocket}; !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected socket paths: got %v want %v", got, want)
			}

			qmpSocketPath, err := manifest.ResolvedQMPSocketPath()
			if err != nil {
				t.Fatalf("resolve qmp socket path: %v", err)
			}
			if qmpSocketPath != tt.wantQMP {
				t.Fatalf("unexpected qmp socket path: got %q want %q", qmpSocketPath, tt.wantQMP)
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
