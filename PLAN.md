# Nix Behavior Change Plan

## Goal

Add a `virtie`-backed launch path for one specific workflow:

- `connectWith = "ssh"`
- `protocol = "virtiofs"`
- airlock disabled
- launch is interactive and foreground-only

This phase does not attempt a full replacement of all current `mkSandbox` host-side launch behavior. It introduces `virtie` for the narrow `virtiofs + ssh` path and leaves every other launch mode on the existing shell-plus-`systemd-run` implementation.

## Scope Boundary

In scope for this phase:

- generating a manifest for the `virtiofs + ssh` launch path
- changing `mkLaunch` to invoke `virtie launch` for that path
- packaging the Go binary in Nix
- testing the new wrapper/manifest contract for that path

Out of scope for this phase:

- console mode
- `9p`
- airlock
- reconnect support
- migrating `mkConnect`
- generic `initExtra` compatibility

## Current Nix Behavior To Change

Today, `mkLaunch` is a generated shell wrapper that:

- sets `REPO_DIR` and `RUNNER_PATH`
- inlines `agentspace.sandbox.initExtra`
- uses `systemctl --user` for preflight and cleanup
- starts `virtiofsd-run`, `microvm-run`, and the SSH session through `systemd-run`

That behavior currently lives mostly in `sandbox-qemu.nix`, with additional launch-time shell coming from modules such as `airlock.nix`.

For this phase, only the built-in `virtiofs + ssh` path moves into `virtie`.

## Planned Nix Changes

### 1. Add a manifest generator for the supported path

Add a Nix-side function that writes a JSON manifest for the `virtiofs + ssh` launch path.

The manifest should include only the fields `virtie` needs in v1:

- `hostName`
- `workingDir`
- `microvmRun`
- `virtiofsdRun`
- `lockPath`
- host directories that must exist before launch
- full SSH argv, including the vsock target and host key bypass flags
- expected virtiofs socket paths

Do not add console, `9p`, airlock, or reconnect fields in this phase.

### 2. Package `virtie`

Add a Nix package for the Go binary and make it available to generated launch wrappers.

The wrapper contract should not depend on a developer-local Go toolchain.

### 3. Change `mkLaunch` only for the supported path

Update `mkLaunch` so that when the sandbox configuration matches the supported workflow, the generated wrapper:

- resolves the manifest path
- sets the working directory as today
- execs `virtie launch <manifest> [-- "$@"]`

For configurations outside the supported workflow, keep the existing launcher behavior unchanged.

This keeps the migration incremental and avoids forcing `virtie` to understand unsupported modes.

### 4. Leave `mkConnect` unchanged

Do not migrate `mkConnect` in this phase.

`virtie` has no separate reconnect command in v1, so the current direct SSH wrapper can remain as-is or be treated as legacy behavior outside the new launch flow.

### 5. Keep unsupported launch logic on the old path

Do not restructure the full contents of `sandbox-qemu.nix` yet.

Instead:

- keep the current shell-based launch path for unsupported configurations
- add a narrow branch that emits the `virtie`-based wrapper and manifest only for the supported configuration

Specifically, do not try to solve these in this phase:

- generic `initExtra` translation
- airlock setup/cleanup
- console attach
- `9p`

### 6. Preserve helper generation in Nix

Keep ownership of these pieces in Nix:

- `microvm.declaredRunner`
- `microvm-run`
- `virtiofsd-run`
- guest-side NixOS configuration

This phase is about lifecycle/orchestration only, not VM command assembly.

## Selection Rule For The New Path

The `virtie` path should only be selected when the config matches the narrow supported workflow.

At minimum:

- `agentspace.sandbox.connectWith == "ssh"`
- `agentspace.sandbox.protocol == "virtiofs"`
- airlock is disabled

If custom launch-time behavior would require `virtie` to understand more than the v1 manifest covers, fall back to the existing launcher.

## Check And Test Updates

### Launch wrapper checks

Add or update checks so the supported-path launcher asserts:

- the generated wrapper calls `virtie launch`
- the wrapper passes the manifest path
- `systemd-run` is not present in that wrapper

For unsupported paths, existing shell-based checks may remain.

### Manifest checks

Add a non-E2E check that validates manifest generation for the supported `virtiofs + ssh` path.

It should assert at least:

- helper binary paths are present
- SSH argv is populated
- expected virtiofs socket paths are present

### Integration coverage

Add one integration path that exercises the generated `virtie`-backed launch wrapper for `virtiofs + ssh`.

The existing console-based E2E path does not need to be migrated in this phase.

### Airlock and console checks

Leave airlock and console checks on the existing launch path for now.

They are not part of the `virtie` rollout in this phase.

## Migration Notes

- Preserve `mkSandbox` as the stable entrypoint for downstream users.
- Keep the rollout additive and gated by configuration.
- Prefer a clean fallback to the old launcher over partial support in `virtie`.
- Do not block this phase on solving `initExtra` as a general extension point.

## Acceptance Criteria

- the supported `virtiofs + ssh` path launches through `virtie launch`
- that path no longer depends on `systemd --user` for lifecycle orchestration
- unsupported paths still work through the existing launcher
- Nix generates the manifest fields required by `virtie` v1
- repo checks validate the new wrapper/manifest contract for the supported path
