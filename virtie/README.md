# virtie

`virtie` is the host-side launcher for the supported `agentspace` sandbox path.

It reads a Nix-generated manifest, starts the required host processes, launches
QEMU, waits for guest SSH readiness, and either prints an out-of-band SSH
command or attaches an active session with `--ssh`. It also handles teardown,
QMP-based shutdown, disk-backed suspend/resume, and runtime vsock CID
allocation.

This is currently a small internal component, not a general-purpose VM runner.
The supported shape today is the built-in `virtiofs + ssh + qemu` flow used by
the main flake.

## Usage

The supported CLI is:

```console
virtie --manifest=MANIFEST [-v|-vv] launch [--ssh] [--resume=no|auto|force] [--always-delete-sockets] [-- <remote-cmd...>]
virtie --manifest=MANIFEST suspend
virtie --manifest=MANIFEST [-v] hotplug ID
virtie --manifest=MANIFEST [-v] hotplug --detach ID
virtie manifest defaults [--resolved]
virtie --manifest=MANIFEST manifest validate
virtie --manifest=MANIFEST manifest resolve
virtie manifest schema
```

In normal use this is invoked by the generated launch wrapper rather than by
hand. See the root flake for packaging and launch integration. Hand-written
manifests may be JSON or TOML; the TOML examples in this directory show a
minimal manifest and a full manifest with default-valued fields included.
`virtie manifest defaults` prints the input manifest defaults as TOML.
`virtie manifest defaults --resolved` prints the internal resolved runtime
manifest defaults as TOML, using placeholder kernel paths because those required
inputs have no defaults. `virtie manifest validate` loads, resolves, and
validates a manifest. `virtie manifest resolve` prints the fully resolved
internal runtime manifest as TOML. A JSON Schema for the manifest input format is
generated at `manifest.schema.json` and available from the `virtie` binary with
`virtie manifest schema`. Regenerate it with
`go run . manifest schema > manifest.schema.json` after manifest shape changes. When
`--manifest` is omitted, `virtie` checks `./manifest.toml` and then
`./manifest.json`. `--manifest` and `-v`/`--verbose` are shared options and may
be placed before or after the subcommand.

## Features

- Loads and validates a manifest for the supported sandbox launch path.
- Accepts JSON and TOML manifests, with Nix-generated manifests remaining JSON.
- Allocates a runtime vsock CID for each session.
- Starts `virtiofsd`, launches QEMU, waits for SSH readiness, and either
  prints the SSH command or attaches the active session with `--ssh`.
- Uses QMP for readiness and graceful shutdown.
- Attaches or detaches typed hotplug devices. Virtiofs includes optional guest
  mount/umount; net and block currently attach only the QEMU-side device.
- Records the active launch PID under the manifest persistence state directory.
  `virtie suspend` validates that PID and sends `SIGTSTP` as a caught control
  signal; the launch process saves QEMU migration state through its existing
  QMP connection, then exits.
- `virtie launch --resume=force` restores from state saved by `virtie suspend`;
  `--resume=auto` restores when saved state exists and otherwise launches fresh.
- Records saved suspend state under the persistence state directory.
- Tears down guest and host-side processes in a defined order on exit or
  signal.

## Exec Templates

Manifest exec arrays render each argv element as a Go `text/template`. The
host process environment is available as `.Env` on every surface.

| Surface | Template values | Injected environment |
| --- | --- | --- |
| `qemu.exec` | `HostName`, `WorkingDir`, `StateDir`, `HostOS`, `HostArch`, `HostSystem`, `.Env` | none |
| `qemu.fwd_tunnel_exec` | `Host`, `Port`, `.Env` | none; QEMU starts the command |
| `ssh.exec` | `CID`, `User`, `Destination`, `.Env` | `CID`, `USER`, `DESTINATION` |
| `mounts/hotplug.mounts[type=virtiofs].virtiofs` | `Socket`, `MountSource`, `MountTag`, `.Env` | `SOCKET`, `MOUNT_SOURCE`, `MOUNT_TAG` |
| `run[].exec` | `CID`, `StateDir`, `Workspace.GuestPath`, `Workspace.HostPath`, user vars, `.Env` | scalar top-level values only |
| `notifications.exec` | `State`, `Message`, notification context values, `.Env` | `STATE`, `MESSAGE`, normalized context values |

## Notes

- The manifest format is owned by this repository and is intentionally narrow.
  It carries evaluated launch facts while `virtie` derives the concrete
  host-side QEMU policy.
- Verbose runtime logs use Go's default `log/slog` handler on stderr, with
  package identity carried as an attribute such as `package=manager`.
- Suspend/resume uses QEMU migration-to-file for disk-backed restore. The
  `SIGTSTP` signal is an internal control shim used by `virtie suspend`, not a
  terminal/job-control suspend. Live pause/resume and `SIGCONT` resume are not
  supported.
- `virtie` assumes the surrounding Nix flow has already resolved guest image
  inputs, package paths, and host capability facts when using generated
  manifests.
- The project is developed with NixOS as the primary target. Some host
  assumptions, including the current QEMU and `virtiofsd` integration, may need
  extra work for macOS.
