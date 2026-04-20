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

## Unsupported Paths

The following launch configurations are intentionally unsupported for now and should fail clearly rather than falling back to legacy host orchestration:

- `connectWith = "console"`
- `protocol = "9p"`
- airlock-enabled launch flows
- custom launch-time `initExtra` behavior beyond the default workspace prelude

These can be added back later, but only after they are redefined in terms of the `virtie`-based launcher.

## Current Nix Behavior To Change

Today, `mkLaunch` is a generated shell wrapper that:

- sets `REPO_DIR` and `RUNNER_PATH`
- inlines `agentspace.sandbox.initExtra`
- starts `virtiofsd`, `microvm-run`, and the SSH session through `virtie`

That behavior currently lives mostly in `sandbox-qemu.nix`, with additional launch-time shell coming from modules such as `airlock.nix`.

For this phase, only the built-in `virtiofs + ssh` path remains available.

## Planned Nix Changes

### 1. Add a manifest generator for the supported path

Add a Nix-side function that writes a JSON manifest for the `virtiofs + ssh` launch path.

The manifest should include only the fields `virtie` needs in v1:

- `hostName`
- `workingDir`
- `microvmRun`
- `lockPath`
- host directories that must exist before launch
- full SSH argv, including the vsock target and host key bypass flags
- per-share virtiofs daemon commands and expected socket paths

Do not add console, `9p`, airlock, or reconnect fields in this phase.

### 2. Package `virtie`

Add a Nix package for the Go binary and make it available to generated launch wrappers.

The wrapper contract should not depend on a developer-local Go toolchain.

### 3. Change `mkLaunch` to always use `virtie`

Update `mkLaunch` so that the generated wrapper:

- resolves the manifest path
- sets the working directory as today
- execs `virtie launch <manifest> [-- "$@"]`

Unsupported configurations must not keep the existing `systemd-run` launcher behavior. They should fail explicitly until equivalent `virtie` support exists.

### 4. Leave `mkConnect` unchanged

Do not migrate `mkConnect` in this phase.

`virtie` has no separate reconnect command in v1, so the current direct SSH wrapper can remain as-is or be treated as legacy behavior outside the new launch flow.

### 5. Remove unsupported launch logic from the active path

Restructure `sandbox-qemu.nix` around the `virtie` workflow instead of keeping a legacy branch alive.

Specifically, do not try to solve these in this phase:

- generic `initExtra` translation
- airlock setup/cleanup
- console attach
- `9p`

Instead, reject them explicitly and keep the main launcher code focused on the supported `virtie` flow.

### 6. Preserve helper generation in Nix

Keep ownership of these pieces in Nix:

- `microvm.declaredRunner`
- `microvm-run`
- `virtiofsd` command generation
- guest-side NixOS configuration

This phase is about lifecycle/orchestration only, not VM command assembly.

## Selection Rule

The supported launcher configuration is:

- `agentspace.sandbox.connectWith == "ssh"`
- `agentspace.sandbox.protocol == "virtiofs"`
- airlock disabled
- default launch-time `initExtra`

Configurations outside that set should fail instead of falling back.

## Check And Test Updates

### Launch wrapper checks

Add or update checks so the launcher asserts:

- the generated wrapper calls `virtie launch`
- the wrapper passes the manifest path
- `systemd-run` is not present in that wrapper

### Manifest checks

Add a non-E2E check that validates manifest generation for the supported `virtiofs + ssh` path.

It should assert at least:

- helper binary paths are present
- SSH argv is populated
- expected virtiofs socket paths are present
- per-share virtiofs daemon commands are present

### Integration coverage

Add one integration path that exercises the generated `virtie`-backed launch wrapper for `virtiofs + ssh`.

Add a check that unsupported launch modes fail explicitly.

## Migration Notes

- Preserve `mkSandbox` as the stable entrypoint for downstream users.
- Prefer a small, explicit supported surface over preserving legacy fallbacks.
- Do not block this phase on solving `initExtra` as a general extension point.

## Acceptance Criteria

- the supported `virtiofs + ssh` path launches through `virtie launch`
- that path no longer depends on `systemd --user`, `supervisord`, or the `modules/virtiofsd.nix` override for lifecycle orchestration
- unsupported paths fail clearly instead of using the old launcher
- Nix generates the manifest fields required by `virtie` v1
- repo checks validate the new wrapper/manifest contract for the supported path
