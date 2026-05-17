# Virtie Manifest

Migration plan for replacing the current virtie manifest contract with the
new `virtie/manifest-proposed.toml` shape.

**Status**: In-Progress

## Goals

Move `virtie` to the proposed manifest format while preserving every
consumer-level sandbox feature.

- Make TOML the documented human-facing manifest format and keep JSON as the
  generated wrapper format with the same field names and structure.
- Replace camelCase and nested historical names with snake_case keys from
  `virtie/manifest-proposed.toml`.
- Remove manifest fields that `virtie` can infer at runtime, such as host
  system facts and a separate runtime directory.
- Keep Nix responsible for evaluated build artifacts and consumer options that
  cannot be rediscovered by `virtie`.
- Preserve current launch behavior for QEMU, SSH readiness, virtiofs and 9p
  shares, volumes, networking, graphics, write files, balloon control,
  notifications, suspend/resume, and generated launch wrappers.

Out of scope:

- Preserving compatibility with the current manifest schema.
- Changing `virtie launch --manifest=MANIFEST`, suspend/resume CLI behavior, or
  the generated launch wrapper UX.
- Adding a non-QEMU backend. The schema should avoid blocking a future backend,
  but implementation remains QEMU-only.
- Changing NixOS guest construction, kernel/initrd generation, or microvm.nix
  option semantics.

Acceptance criteria:

- [ ] `virtie/manifest-proposed.toml` is promoted into the supported example
      set and loads successfully.
- [ ] Nix-generated `manifest.json` uses the same snake_case shape as the new
      TOML format.
- [ ] Direct manifest loading accepts the new TOML and JSON shapes and rejects
      the old shape with clear errors.
- [ ] Existing consumer-facing features remain represented in generated
      manifests and covered by checks.
- [ ] `MIGRATION.md` documents the breaking manifest contract change.

## Progress

- [x] Draft `virtie/manifest-proposed.toml`.
- [x] Convert the proposal to snake_case, command `exec = [...]` lists, and the
      inline `{ min, max }` range shape.
- [x] Restore dropped consumer-level fields in the proposal: read-only volumes
      and mounts, volume label/direct/serial, mount type/security/cache,
      machine id, serial console, graphics, QEMU user/sockets, and write-file
      ownership/source.
- [ ] Finalize the proposed manifest field names and remove remaining TODOs.
- [ ] Replace the Go public `Document` schema with the new shape.
- [ ] Update Nix manifest generation to emit the new JSON shape.
- [ ] Update examples, migration notes, and checks.

## Implementation Plan

### Schema

Implement a new public document shape matching `virtie/manifest-proposed.toml`.
Use snake_case `json` and `toml` tags so generated JSON and hand-written TOML
are structurally identical.

Top-level fields:

- `host_name`, `working_dir`, and `state_dir`.
- `host` should be temporary or removed before final implementation if host
  facts are fully inferred at runtime. `host.netcat` should remain only until
  guest-to-host forwarding no longer shells out through `cmd:netcat`.
- `qemu` with `exec`, optional `user`, `seccomp`, `machine_options`,
  `qmp_socket`, and `guest_agent_socket`.
- `machine` with `type`, optional `id`, `memory`, optional `vcpu`, optional
  `cpu`, and `kvm`.
- `kernel` with `path`, `initrd_path`, `params`, and `serial_console`.
- `graphics` with `backend = "headless" | "gtk" | "cocoa"`.
- `ssh` with `exec`, `user`, `ready_socket`, and `retry_delay_ms`.
- `vsock.cid_range = { min, max }`.
- `volumes[]`, `mounts[]`, `networks[]`, `balloon`, `write_files[]`, and
  `notifications`.

Lower the new public document into the existing internal `Manifest` and `QEMU`
runtime structures first. Keep manager and QEMU argv generation behavior stable
unless a field was intentionally removed from the contract.

### Generated Manifest

Update `sandbox-qemu.nix` so `agentspace.sandbox.launch.virtieManifestData`
emits the new JSON shape, not the old camelCase shape.

Preserve all current consumer options:

- `agentspace.sandbox.hostName` maps to `host_name`.
- Persistence paths map to `state_dir` and volume image paths. Do not emit a
  separate `runtime_dir`; relative sockets resolve under `state_dir`.
- SSH argv and identity file options map to `ssh.exec`.
- QEMU package, user, seccomp support, extra args, machine opts, and socket
  names map under `qemu`.
- Machine memory/vCPU, CPU override, machine id, kernel paths/params, serial
  console, graphics backend, balloon options, volumes, shares, write files,
  notifications, interfaces, and forward ports keep their existing behavior.

Generated JSON must use the same names as TOML, for example `read_only`,
`security_model`, `write_files`, `guest_path`, `retry_delay_ms`, and
`cid_range`.

### Feature Preservation Checklist

The migration must preserve:

- Default sandbox launch with SSH attach and non-attach modes.
- Persistent home/store overlay volumes, auto-created ext4 volumes, read-only
  store disks, volume labels, direct/cache behavior, and block serials.
- Workspace share, read-only Nix store share, extra virtiofs shares, external
  virtiofs sockets, and opt-in 9p shares.
- QMP, QGA, and SSH-ready socket behavior under the new `state_dir` policy.
- Host-to-guest forward ports and guest-to-host forward ports until the netcat
  TODO is resolved.
- Graphical GTK/Cocoa manifests and default headless operation.
- Runtime vsock CID allocation and suspend/resume state.
- Guest write files using inline text or host source files, with chown, mode,
  and overwrite behavior.
- Balloon device defaults, controller thresholds, and notification hooks.
- QEMU passthrough args and optional QEMU user privilege drop.

## Validation

- Update Go tests that load TOML examples and add new JSON/TOML parity fixtures
  for the proposed shape.
- Update `checks/virtie-manifest.nix` assertions for generated JSON names and
  feature-rich scenarios.
- Run `CGO_ENABLED=0 go test ./...` from `virtie`.
- Run relevant Nix checks for manifest contract, fake-tools E2E launch,
  consumer workflow, graphical checks, suspend/resume, write files,
  notifications, extra shares, external store socket, and balloon behavior.
- Clean up any `./result` symlink from Nix builds with `unlink result`.

## Appendix

Breaking change policy:

- Do not maintain old-schema compatibility unless a test or migration need
  proves it is necessary. Direct manifest producers should move to the new
  snake_case shape.
- Document the mapping in `MIGRATION.md` with examples for the most visible
  renames: `identity.hostName -> host_name`, `paths.workingDir -> working_dir`,
  `qemu.binaryPath + extraArgs -> qemu.exec`, `memory.sizeMiB -> machine.memory`,
  `volumes[].imagePath -> volumes[].image`, `mounts[].sourcePath ->
  mounts[].source`, and `writeFiles[].guestPath -> write_files[].guest_path`.

Open design follow-ups:

- Remove `host.system` from the final schema if runtime detection via
  `runtime.GOOS` and `runtime.GOARCH` covers all current policy branches.
- Replace guest-to-host `guestfwd cmd:netcat` lowering with QEMU-native
  forwarding or chardev handling, then remove `host.netcat`.
- Decide whether `graphics.backend = "headless"` remains explicit or whether
  omitting `[graphics]` is the only headless representation.
- Decide whether `qemu.exec` should include only the executable plus passthrough
  args, or also generated arguments in any future debugging/export mode.
