# virtie

`virtie` is the host-side launcher for the supported `agentspace` sandbox path.

It reads a Nix-generated manifest, starts the required host processes, launches
QEMU, waits for guest SSH readiness, and attaches the active session. It also
handles teardown, QMP-based shutdown, keep-alive pause/resume, and runtime vsock
CID allocation.

This is currently a small internal component, not a general-purpose VM runner.
The supported shape today is the built-in `virtiofs + ssh + qemu` flow used by
the main flake.

## Usage

The supported CLI is:

```console
virtie launch --manifest=MANIFEST [-- <remote-cmd...>]
virtie suspend --manifest=MANIFEST
virtie resume --manifest=MANIFEST
```

In normal use this is invoked by the generated launch wrapper rather than by
hand. See the root flake for packaging and launch integration.

## Features

- Loads and validates the manifest for the supported sandbox launch path.
- Allocates a runtime vsock CID for each session.
- Starts `virtiofsd`, launches QEMU, waits for SSH readiness, and attaches the
  active session.
- Uses QMP for readiness and graceful shutdown.
- Records the active launch PID at `<workingDir>/.virtie/<hostName>.pid`.
  `virtie suspend` sends `SIGTSTP` to that process, and `virtie resume` sends
  `SIGCONT`; only the launch process talks to QMP.
- Records advisory suspend state at
  `<workingDir>/.virtie/<hostName>.suspend.json`; QMP status remains
  authoritative.
- Tears down guest and host-side processes in a defined order on exit or
  signal.

## Notes

- The manifest format is owned by this repository and is intentionally narrow.
- Suspend/resume is a pause/resume lifecycle for a still-running QEMU process,
  not full RAM/device hibernation and restore.
- `SIGTSTP` is used for suspend control because `SIGSTOP` cannot be caught by
  the launch process before it stops itself.
- `virtie` currently assumes the surrounding Nix flow has already resolved the
  guest image inputs and host-side launch settings.
- The project is developed with NixOS as the primary target. Some host
  assumptions, including the current QEMU and `virtiofsd` integration, may need
  extra work for macOS.
