# Agentspace Graphical Mode

**Status**: In-Progress

## Goals

Support graphical QEMU launches through the existing `microvm.graphics` public
API while keeping `virtie` responsible for final QEMU argv compilation.

- Let consumers enable graphics with `microvm.graphics.enable = true` through
  `agentspace.sandbox.extraModules` or the top-level `extraModules` hook.
- Reuse microvm.nix's host-default backend selection: `gtk` on Linux and
  `cocoa` on Darwin.
- Keep the default sandbox headless and preserve the current SSH launch flow.
- Lower graphics into the typed `virtie` manifest instead of requiring
  consumers to use raw QEMU passthrough args.
- Add focused unit and manifest-contract coverage, plus a separate opt-in
  graphical integration check.

Out of scope:

- VNC, SPICE, GPU passthrough, and custom display socket APIs.
- A new `agentspace.sandbox.graphics` wrapper API unless direct
  `microvm.graphics` support proves blocked.
- Running graphical integration tests in the default `nix flake check` set.

## Public API

Consumers opt into graphics using the upstream microvm option schema:

```nix
mkSandbox {
  extraModules = [
    {
      microvm.graphics.enable = true;
      # Optional; defaults to "gtk" on Linux and "cocoa" on Darwin.
      # microvm.graphics.backend = "gtk";
    }
  ];
}
```

The supported v1 backends are:

- `gtk`: Linux default, lowered to QEMU's GTK display path.
- `cocoa`: Darwin default, lowered to QEMU's Cocoa display path.

`microvm.graphics.socket` remains unused for the QEMU backend, matching the
current upstream microvm QEMU runner behavior.

## Runtime Contract

When graphics are disabled, Nix emits `qemu.knobs.noGraphic = true` and `virtie`
keeps passing `-nographic`.

When graphics are enabled, Nix emits `qemu.knobs.noGraphic = false` and an
optional typed `qemu.graphics.backend` field. `virtie` validates the backend and
adds the display, GPU, USB tablet, and USB keyboard QEMU devices.

For `gtk`, `virtie` emits:

```console
-display gtk,gl=off
-device virtio-vga
-device qemu-xhci
-device usb-tablet
-device usb-kbd
```

For `cocoa`, `virtie` emits:

```console
-display cocoa
-device virtio-gpu
-device qemu-xhci
-device usb-tablet
-device usb-kbd
```

The guest-side graphics support continues to come from microvm.nix's graphics
module, including `drm` and `virtio_gpu`.

## Test Plan

- Manifest contract checks: keep default checks lightweight for Nix lowering,
  and keep the full graphical sandbox/module assertion in `checks/graphical.nix`.
- Go unit tests: validate accepted/rejected graphics backends and assert QEMU
  args for disabled, `gtk`, and `cocoa` graphics.
- Opt-in E2E: add `checks/graphical.nix`, separate from default checks, that
  builds and boots a small graphical sandbox under a host display shim and uses
  SSH to assert guest graphics readiness before shutting down.
  It is exposed as
  `.#legacyPackages.x86_64-linux.graphicalChecks.graphical-real-boot-smoke`
  rather than under `checks` so `nix flake check` does not run a real
  graphical VM by default. Because it boots a real QEMU VM and needs host
  devices such as `/dev/vhost-vsock`, run it with Nix sandboxing disabled:
  `nix build .#legacyPackages.x86_64-linux.graphicalChecks.graphical-real-boot-smoke --option sandbox false`.

## Progress

- [x] Remove the direct `microvm.graphics.enable = false` guardrail.
- [x] Lower graphical settings into the typed `virtie` manifest.
- [x] Compile graphical QEMU display args inside `virtie`.
- [x] Add contract and unit tests.
- [x] Add an opt-in graphical integration check.
