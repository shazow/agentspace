# Virtie Input Checking

Survey of places where `virtie` still pre-validates manifest or command inputs, with candidates for more pass-through behavior that would let the eventual consumer of the value report the failure instead.

**Status**: Proposed

## Goals

- Keep validation only where `virtie` must understand a value to choose control flow, protect its own state, or avoid ambiguous host-side side effects.
- Let QEMU, host tools, guest tools, and QGA report invalid values when `virtie` is only forwarding those values.
- Remove obsolete tests that assert narrow preflight error strings for values that should pass through to the downstream tool.
- Preserve stage-aware wrapping so downstream failures still identify whether they came from preflight, QEMU launch, guest file writes, restore, suspend, or teardown.

Out of scope:

- Silently ignoring malformed configuration.
- Removing JSON decoding errors, file I/O errors, lock validation, suspend-state validation, QMP protocol errors, or process lifecycle errors.
- Full `microvm-run` parity or arbitrary QEMU argv replacement.

## Checklist

Estimates are rough net line-count impact after gofmt and normal test cleanup. Negative numbers mean expected deletion. The current already-completed QEMU device validation cleanup is included as context but not counted in future impact.

### Done

- [x] Relax QEMU device payload validation.
  QEMU device IDs and direct payload fields now pass through manifest validation for RNG, VSOCK, balloon, 9p, block, and network devices. Virtiofs still requires `socketPath` because `virtie` waits for that socket before QEMU starts. Remaining device validation is mostly transport enum checking because `virtie` still maps `pci`, `mmio`, and `ccw` before QEMU starts.
  Estimated impact already taken: about -10 net LOC, with roughly -55 validation LOC and +45 replacement test LOC.

- [x] Remove notification command path preflight validation.
  Notification hooks are best-effort. If `notifications.command.path` is empty or wrong, the notification runner fails under the existing best-effort notification policy instead of blocking manifest load.
  Estimated impact already taken: about -20 net LOC.

- [x] Relax provided `qemu.smp.cpus` validation.
  Omitted CPUs still resolve from the host runtime. Provided values are emitted into `-smp` for QEMU to accept or reject.
  Estimated impact already taken: about -10 net LOC.

- [x] Relax QEMU scalar validation in `Manifest.Validate`.
  Let QEMU reject `qemu.name`, `qemu.machine.type`, `qemu.cpu.model`, `qemu.kernel.path`, `qemu.kernel.initrdPath`, and `qemu.memory.sizeMiB` when they are invalid. Keep path resolution behavior unchanged; the values should still be emitted into QEMU args.
  Estimated impact already taken: about -15 net LOC.

- [x] Narrow `volumes[].imagePath` validation to `autoCreate` volumes.
  If `virtie` is not creating the image, leave bad paths to QEMU or the downstream block-device consumer. Keep positive size and image path checks for `autoCreate` because `virtie` creates, truncates, and formats that file.
  Estimated impact already taken: about +20 net LOC with explicit auto-create coverage.

### High-Confidence Cleanup

- [x] Complete high-confidence validation cleanup.
  Remaining unchecked items are mode/schema/control-flow dependent rather than straightforward passthrough deletions.

### Medium-Confidence Cleanup

- [ ] Defer SSH validation based on launch mode.
  Validate `ssh.argv` and `ssh.user` only when `--ssh`, a remote command, or printed SSH hints need them. A pure non-SSH VM launch path could avoid requiring SSH fields.
  Estimated impact: +10 to -30 net LOC. This may add option-aware validation or move checks out of `Manifest.Validate`, so the net savings depend on how much CLI/manager plumbing is needed.

- [ ] Defer `qemu.guestAgent.socketPath` validation for `writeFiles`.
  Instead of rejecting manifests with `writeFiles` but no QGA socket during load, allow launch to fail at the guest-agent stage when it cannot resolve or connect to the socket.
  Estimated impact: -5 to -20 net LOC. Small validation deletion, but tests may need to move from manifest validation to staged runtime failure.

- [ ] Reconsider `qemu.sshReady.socketPath` as launch-mode-specific.
  Fresh launch currently depends on guest-pushed readiness, so this only becomes pass-through if a mode exists that does not wait for the SSH-ready port.
  Estimated impact: unknown until mode semantics exist; likely +20 LOC if a new mode is introduced, or no change if kept required.

- [ ] Rework `qemu.memory.backend` from enum to passthrough.
  Current code owns a switch for `""`, `"default"`, and `"memfd"`. To make this pass-through, replace it with explicit QEMU memory object args or a Nix-lowered `passthroughArgs` fragment.
  Estimated impact: -15 to +40 net LOC. Removing the enum is small, but changing the manifest shape may require tests, Nix updates, and `MIGRATION.md`.

### Keep As Virtie-Owned

- [ ] Keep JSON decoding and trailing-data rejection.
  Estimated impact: 0 LOC.

- [ ] Keep manifest `nil` checks and defaulting.
  Estimated impact: 0 LOC.

- [ ] Keep `paths.workingDir` while relative path resolution exists.
  Empty working directories would make multiple relative path policies ambiguous.
  Estimated impact: 0 LOC.

- [ ] Keep `paths.lockPath`, VSock CID range, lock ownership checks, launch PID parsing, and suspend-state validation.
  These protect virtie's own concurrency and suspend/resume state, not downstream tool inputs.
  Estimated impact: 0 LOC.

- [ ] Keep QEMU transport validation while the manifest uses abstract `pci`/`mmio`/`ccw`.
  This can become pass-through only if the manifest stores concrete QEMU driver names or complete device arg fragments.
  Estimated impact: 0 LOC for now.

- [ ] Keep `qemu.qmp.socketPath` required for current launch semantics.
  Fresh launch needs QMP for monitor readiness, shutdown, suspend, and resume.
  Estimated impact: 0 LOC.

- [ ] Keep `virtiofs.daemons[]` command/socket/tag validation.
  `virtie` starts these processes and waits on their sockets. The tag-to-share check remains useful while virtiofs shares and daemons are split across manifest sections.
  Estimated impact: 0 LOC.

- [ ] Keep `qemu.devices.virtiofs[].socketPath` validation.
  `virtie` resolves and waits for every virtiofs device socket before building QEMU args, so an empty value is not QEMU passthrough.
  Estimated impact: 0 LOC.

- [ ] Keep `writeFiles` payload exclusivity and absolute guest path validation.
  `virtie` must choose the payload source and uses `path.Dir` before invoking guest commands.
  Estimated impact: 0 LOC.

- [ ] Keep minimal `writeFiles.*.*` field validation.
  File-backed host paths must be non-empty, and chmod modes must be three- or four-digit octal strings. These failures otherwise occur during the guest-file-write stage after the VM is already booting.
  Estimated impact: 0 LOC.

- [ ] Keep balloon controller validation.
  Bounds, thresholds, poll interval, step size, and reclaim holdoff are consumed by virtie's controller, not QEMU.
  Estimated impact: 0 LOC.

- [ ] Keep resume mode normalization and SSH readiness token validation.
  These are virtie control-flow/protocol checks.
  Estimated impact: 0 LOC.

## LOC Impact Summary

- High-confidence cleanup remaining: 0 LOC.
- Medium-confidence cleanup: about -10 to +90 net LOC, depending on whether launch-mode or manifest-shape changes are introduced.
- Total likely near-term cleanup without changing public schema: complete, about -65 net LOC including completed items.
- Total possible cleanup with schema/control-flow changes: about -100 to -250 LOC, but with higher migration and test-update cost.

## Implementation Plan

- Split validation into two named layers:
  - `ValidateRuntimeContract` for values `virtie` must consume safely.
  - QEMU/tool passthrough values resolved and emitted without semantic validation.
- Keep QEMU scalar values in `Manifest.Validate` only when `virtie` consumes them before QEMU.
- Keep builder-time errors only for manifest abstractions that still need lowering, such as transport and the current memory backend enum.
- Keep notification command values as passthrough, but keep minimal write-file field validation for failures that would otherwise happen during guest VM runtime.
- Keep volume validation narrowed to `autoCreate` volumes only.
- Update tests by deleting cases that assert rejected passthrough inputs and adding a small number of positive tests proving those inputs reach the generated QEMU args or runtime stage unchanged.

## Test Cleanup Targets

- Completed: removed manifest tests that expect preflight errors for QEMU scalar values that QEMU should reject.
- Completed: removed notification command path preflight rejection tests now that empty command paths are best-effort notification failures.
- Keep tests for:
  - transport rejection while the abstract enum remains.
  - write-file source exclusivity and absolute guest paths.
  - auto-created volume size/path requirements.
  - balloon controller validation.
  - suspend/lock/PID validation.
  - stage wrapping for downstream QEMU, host tool, guest tool, and QGA failures.

## Acceptance Criteria

- `virtie` validates only values it must interpret or use before invoking a downstream component.
- Invalid QEMU-owned scalar and device values are allowed through manifest loading and fail during QEMU startup.
- Invalid host notification command values do not fail launch preflight.
- Invalid guest file modes fail during manifest validation, before guest VM runtime begins.
- Tests no longer assert redundant preflight diagnostics for QEMU or notification exec behavior.
- `cd virtie && CGO_ENABLED=0 go test ./...` passes.
- Relevant Nix manifest checks still pass, and any `result` symlink from Nix builds is removed.
