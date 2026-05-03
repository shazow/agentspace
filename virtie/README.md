# virtie

`virtie` is the host-side launcher for the supported `agentspace` sandbox path
and for direct JSON manifests that provide their own VM artifacts.

It reads a Nix-generated manifest, starts the required host processes, launches
QEMU, waits for guest SSH readiness, and either prints an out-of-band SSH
command or attaches an active session with `--ssh`. It also handles teardown,
QMP-based shutdown, disk-backed suspend/resume, and runtime vsock CID
allocation.

This is currently a small launcher, not a general-purpose VM builder. It does
not download or create guest images; the manifest points at existing QEMU,
kernel, initrd, and disk image paths.

## Usage

The supported CLI is:

```console
virtie launch --manifest=MANIFEST [--ssh] [--resume=no|auto|force] [-- <remote-cmd...>]
virtie suspend --manifest=MANIFEST
```

In normal use this is invoked by the generated launch wrapper rather than by
hand. See the root flake for packaging and launch integration.

## Standalone manifest

`virtie` can also be run without the agentspace Nix runner when the guest
artifacts already exist. A minimal standalone manifest may omit `virtiofs`
shares entirely:

```json
{
  "identity": {
    "hostName": "alpine-vm"
  },
  "paths": {
    "workingDir": ".",
    "lockPath": ".virtie/alpine-vm.lock",
    "runtimeDir": ""
  },
  "persistence": {
    "directories": [".virtie"],
    "baseDir": ".virtie",
    "stateDir": ".virtie"
  },
  "ssh": {
    "argv": [
      "/usr/bin/ssh",
      "-q",
      "-o",
      "StrictHostKeyChecking=no",
      "-o",
      "UserKnownHostsFile=/dev/null"
    ],
    "user": "agent"
  },
  "qemu": {
    "binaryPath": "/usr/bin/qemu-system-x86_64",
    "name": "alpine-vm",
    "machine": {
      "type": "microvm",
      "options": ["accel=kvm:tcg", "acpi=on", "pcie=off"]
    },
    "cpu": {
      "model": "host",
      "enableKvm": true
    },
    "memory": {
      "sizeMiB": 1024
    },
    "kernel": {
      "path": "artifacts/vmlinuz",
      "initrdPath": "artifacts/initramfs",
      "params": "console=ttyS0 reboot=t panic=-1 root=/dev/vda rw"
    },
    "smp": {
      "cpus": 2
    },
    "console": {
      "stdioChardev": true,
      "serialConsole": true
    },
    "knobs": {
      "noDefaults": true,
      "noUserConfig": true,
      "noReboot": true,
      "noGraphic": true
    },
    "qmp": {
      "socketPath": "qmp.sock"
    },
    "devices": {
      "rng": {
        "id": "rng0",
        "transport": "pci"
      },
      "block": [
        {
          "id": "vda",
          "imagePath": "artifacts/rootfs.raw",
          "aio": "threads",
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
    "daemons": []
  }
}
```

The guest must be prepared separately. For SSH attach to work, it must boot the
specified kernel/initrd/root disk, run `sshd`, and accept connections at the
runtime vsock CID that `virtie` appends as `agent@vsock/<cid>`. If `writeFiles`
is configured, the guest must also run QEMU guest agent on the manifest's
`qemu.guestAgent.socketPath`.

The agentspace Nix wrapper also has an experimental Alpine image path via
`mkSandbox { alpine = true; }`. That path builds the guest artifacts in Nix and
emits the `virtie` manifest for the generated kernel, initrd, and root disk.

## Features

- Loads and validates the manifest for the supported sandbox launch path.
- Allocates a runtime vsock CID for each session.
- Starts configured `virtiofsd` daemons, launches QEMU, waits for SSH
  readiness, and either prints the SSH command or attaches the active session
  with `--ssh`.
- Uses QMP for readiness and graceful shutdown.
- Records the active launch PID under the manifest persistence state directory.
  `virtie suspend` validates that PID and sends `SIGTSTP` as a caught control
  signal; the launch process saves QEMU migration state through its existing
  QMP connection, then exits.
- `virtie launch --resume=force` restores from state saved by `virtie suspend`;
  `--resume=auto` restores when saved state exists and otherwise launches fresh.
- Records saved suspend state under the persistence state directory.
- Tears down guest and host-side processes in a defined order on exit or
  signal.

## Notes

- The manifest format is owned by this repository and is intentionally narrow.
- Suspend/resume uses QEMU migration-to-file for disk-backed restore. The
  `SIGTSTP` signal is an internal control shim used by `virtie suspend`, not a
  terminal/job-control suspend. Live pause/resume and `SIGCONT` resume are not
  supported.
- For standalone manifests, the caller owns guest artifact creation and the
  guest-side SSH/vsock setup.
- The project is developed with NixOS as the primary target. Some host
  assumptions, including the current QEMU and `virtiofsd` integration, may need
  extra work for macOS.
