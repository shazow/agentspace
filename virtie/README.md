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
virtie launch --manifest=MANIFEST [--ssh] [--resume=no|auto|force] [-v|-vv] [-- <remote-cmd...>]
virtie suspend --manifest=MANIFEST
```

In normal use this is invoked by the generated launch wrapper rather than by
hand. See the root flake for packaging and launch integration.

## Features

- Loads and validates the manifest for the supported sandbox launch path.
- Allocates a runtime vsock CID for each session.
- Starts `virtiofsd`, launches QEMU, waits for SSH readiness, and either
  prints the SSH command or attaches the active session with `--ssh`.
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
- Verbose runtime logs use stdlib `log/slog` text output on stderr, with
  package identity carried as an attribute such as `package=manager`.
- Suspend/resume uses QEMU migration-to-file for disk-backed restore. The
  `SIGTSTP` signal is an internal control shim used by `virtie suspend`, not a
  terminal/job-control suspend. Live pause/resume and `SIGCONT` resume are not
  supported.
- `virtie` currently assumes the surrounding Nix flow has already resolved the
  guest image inputs and host-side launch settings.
- The project is developed with NixOS as the primary target. Some host
  assumptions, including the current QEMU and `virtiofsd` integration, may need
  extra work for macOS.
