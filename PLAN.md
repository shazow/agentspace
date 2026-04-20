# Nix Behavior Change Plan

## Goal

Make `virtie` the only host-side launch path for the currently supported sandbox workflow:

- `connectWith = "ssh"`
- `protocol = "virtiofs"`
- airlock disabled
- launch is interactive and foreground-only

This phase does not attempt to preserve every historical launch mode. Instead, it removes unsupported launch paths from active use, keeps `sandbox-qemu.nix` centered on the `virtie` workflow, and leaves additional functionality to be added back later on top of that base.

## Scope Boundary

In scope for this phase:

- generating a manifest for the `virtiofs + ssh` launch path
- changing `mkLaunch` to always invoke `virtie launch`
- packaging the Go binary in Nix
- testing the new wrapper/manifest contract for that path
- explicitly rejecting unsupported launch configurations instead of falling back

Out of scope for this phase:

- console mode
- `9p`
- airlock
- reconnect support
- migrating `mkConnect`
- generic `initExtra` compatibility

## Status

Implemented so far:

- `mkLaunch` now always execs `virtie launch <manifest>`
- the manifest now carries per-share `virtiofsd` commands and socket paths
- `virtie` now starts `virtiofsd` directly instead of launching a `virtiofsd-run` helper
- `modules/virtiofsd.nix` has been removed from the flake module list
- unsupported launch paths now fail with explicit assertions instead of falling back
- the public `agentspace.sandbox.extraModules` hook has been restored via a follow-up module-extension pass in `mkSandbox`
- checks now cover the launch wrapper contract, sandbox-safe `virtie` e2e, unsupported-path rejection, home-manager, and `extraModules`

Still open:

- the default `mkSandbox {}` path currently defaults to `connectWith = "ssh"` without provisioning credentials, so the out-of-the-box default launcher remains broken until that is fixed
- `mkConnect` is still a direct SSH wrapper rather than a `virtie` subcommand
- console, `9p`, airlock, and general `initExtra` support are still intentionally removed and would need to be reintroduced on top of the current `virtie` base

## Unsupported Paths

The following launch configurations are intentionally unsupported for now and should fail clearly rather than falling back to legacy host orchestration:

- `connectWith = "console"`
- `protocol = "9p"`
- airlock-enabled launch flows
- custom launch-time `initExtra` behavior beyond the default workspace prelude

These can be added back later, but only after they are redefined in terms of the `virtie`-based launcher.

## Current State

Today, `mkLaunch` is a generated shell wrapper that:

- sets `REPO_DIR`
- inlines the supported `agentspace.sandbox.initExtra` prelude
- execs `virtie launch <manifest>`

For the supported path, the manifest generated from `sandbox-qemu.nix` now includes:

- `hostName`
- `workingDir`
- `microvmRun`
- `lockPath`
- host directories that must exist before launch
- full SSH argv, including the vsock target and host key bypass flags
- per-share virtiofs daemon commands and expected socket paths

`virtie` consumes that manifest, starts the configured `virtiofsd` daemons directly, waits for sockets, starts `microvm-run`, retries SSH readiness, and then attaches the interactive SSH session.

`mkConnect` remains a direct SSH wrapper, and unsupported launch modes are rejected by assertions in `sandbox-qemu.nix`.

## Implemented Changes

### 1. Manifest generator for the supported path

Nix now writes a JSON manifest for the supported `virtiofs + ssh` launch path.

The manifest intentionally includes only the fields `virtie` needs in v1:

- `hostName`
- `workingDir`
- `microvmRun`
- `lockPath`
- required host directories
- SSH argv
- per-share virtiofs daemon commands and socket paths

### 2. `virtie` packaged in Nix

The Go binary is now packaged in the flake and used by generated launch wrappers.

The wrapper contract does not depend on a developer-local Go toolchain.

### 3. `mkLaunch` now always uses `virtie`

The generated launcher now:

- resolves the manifest path through the generated config
- sets the working directory as today
- execs `virtie launch <manifest> [-- "$@"]`

Unsupported configurations no longer keep the legacy `systemd-run` launcher behavior.

### 4. `mkConnect` still unchanged

`virtie` still has no reconnect command in v1, so the current direct SSH wrapper remains in place.

### 5. Unsupported launch logic removed from the active path

`sandbox-qemu.nix` is now centered on the `virtie` workflow instead of carrying a live legacy branch.

These are currently rejected explicitly:

- generic `initExtra` translation
- airlock setup and cleanup
- console attach
- `9p`

### 6. Helper generation preserved in Nix

Nix still owns:

- `microvm.declaredRunner`
- `microvm-run`
- `virtiofsd` command generation
- guest-side NixOS configuration

Lifecycle orchestration moved to `virtie`, but VM command assembly did not.

### 7. `agentspace.sandbox.extraModules` restored

`mkSandbox` now does an initial evaluation pass, reads `config.agentspace.sandbox.extraModules`, and extends the system with those modules.

That restores the public option for downstream modules that set `agentspace.sandbox.extraModules` instead of only passing `extraModules` as a top-level `mkSandbox` argument.

## Selection Rule

The supported launcher configuration is:

- `agentspace.sandbox.connectWith == "ssh"`
- `agentspace.sandbox.protocol == "virtiofs"`
- airlock disabled
- default launch-time `initExtra`

Configurations outside that set should fail instead of falling back.

## Check And Test Updates

### Launch wrapper checks

Checks now assert:

- the generated wrapper calls `virtie launch`
- the wrapper passes the manifest path
- `systemd-run` is not present in that wrapper
- the runner's `virtiofsd-run` filename is only a stub and does not pull in `supervisord`

### Manifest checks

There is now a non-E2E check that validates manifest generation for the supported `virtiofs + ssh` path.

It asserts at least:

- helper binary paths are present
- SSH argv is populated
- expected virtiofs socket paths are present
- per-share virtiofs daemon commands are present

### Integration coverage

There is now one integration path that exercises the generated `virtie`-backed launch wrapper for `virtiofs + ssh`.

There are also checks for unsupported launch modes and for `agentspace.sandbox.extraModules`.

## Migration Notes

- Preserve `mkSandbox` as the stable entrypoint for downstream users.
- Prefer a small, explicit supported surface over preserving legacy fallbacks.
- Do not block this phase on solving `initExtra` as a general extension point.

## Acceptance Criteria

- done: the supported `virtiofs + ssh` path launches through `virtie launch`
- done: that path no longer depends on `systemd --user`, `supervisord`, or the `modules/virtiofsd.nix` override for lifecycle orchestration
- done: unsupported paths fail clearly instead of using the old launcher
- done: Nix generates the manifest fields required by `virtie` v1
- done: repo checks validate the new wrapper/manifest contract for the supported path
- open: the default `mkSandbox {}` launch path still needs a usable out-of-the-box SSH login story
