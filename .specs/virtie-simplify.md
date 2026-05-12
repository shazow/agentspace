# Virtie Simplified Policy Boundary

Proposal to simplify the sandbox launcher by moving host-side QEMU policy from
Nix into `virtie`.

**Status**: Proposal

## Goals

Replace the fully resolved `manifest.qemu` contract with a thinner manifest of
evaluated launch facts, then make `virtie` derive the supported QEMU policy.

- Remove `agentspace-qemu-config.nix` as a policy owner, not by inlining it into
  another Nix file.
- Keep Nix responsible for evaluating `microvm.nix`, producing guest artifacts,
  and passing facts that `virtie` cannot rediscover locally.
- Move host-side policy composition into Go: transport selection, machine
  defaults, CPU defaults, device IDs, block letters, network lowering, memory
  backend selection, and similar launch-time decisions.
- Preserve the current supported launcher behavior for `virtiofs + ssh + qemu`,
  including dynamic vsock CID allocation, foreground process management, QMP
  lifecycle, and SSH attach behavior.
- Prove parity against the current typed-manifest path before deleting the Nix
  policy builder.

Out of scope:

- changing the public `virtie launch --manifest=MANIFEST` CLI
- moving guest image construction, kernel builds, initrd generation, or NixOS
  module evaluation out of Nix
- broadening the supported launcher surface beyond the current admitted path
- deleting supported features such as extra shares, graphical mode, balloon
  device support, notifications, or passthrough QEMU args as part of this
  simplification

## Proposed Boundary

Nix should emit evaluated facts and package capabilities, while `virtie` should
own policy derived from those facts.

Manifest facts that should remain Nix-owned:

- QEMU binary path, kernel path, initrd path, and guest hostname.
- Host platform facts needed for launch policy, such as system architecture,
  operating system family, and selected QEMU package capabilities like seccomp
  support.
- Evaluated memory, vCPU, machine, CPU, serial console, graphics, and machine ID
  inputs from `microvm.nix`.
- Evaluated volumes, shares, network interfaces, forward ports, virtiofsd
  commands, persistence paths, SSH settings, write-file requests, notifications,
  and passthrough QEMU args.

Policy that should move to `virtie`:

- Machine option defaults and accelerator selection.
- PCI versus MMIO transport selection.
- CPU model defaulting and KVM enablement policy.
- Kernel console parameter construction and standard panic or reboot params.
- Shared-memory backend selection for virtiofs.
- QMP, guest-agent, and SSH-ready socket defaults.
- Device ID assignment for rng, virtiofs, 9p, block, network, and vsock devices.
- Block drive letters, AIO engine, cache policy, and serial propagation.
- User-network forward lowering, ROM-file policy, and multiqueue vector policy.
- Optional graphical QEMU display device lowering for supported backends.

## Implementation Plan

- Add a new manifest version or compatibility discriminator so `virtie` can
  distinguish the current fully resolved `qemu` contract from the thinner facts
  contract during migration.
- Introduce Go types for the thinner facts contract without mirroring raw
  `microvm.nix` option internals one-for-one.
- Add a Go policy compiler that converts the thinner facts into the existing
  internal `manifest.QEMU` shape, then reuse the current QEMU argv builder.
- Keep the current Nix-generated `manifest.qemu` path as the parity oracle while
  the new compiler is developed.
- Update `sandbox-qemu.nix` to emit the thinner facts contract once the parity
  suite covers representative launch configurations.
- Delete `agentspace-qemu-config.nix` only after the new manifest path is used by
  the generated launch wrapper and parity checks pass.
- Update `.specs/agentspace.md`, `.specs/virtie.md`, and `MIGRATION.md` if the
  manifest contract changes for direct manifest producers.

## Validation

Add parity checks that compare the effective QEMU invocation from the current
typed-manifest path against the new `virtie` policy compiler.

Required scenarios:

- default sandbox
- serial console enabled
- forward ports configured
- explicit CPU or machine overrides
- fixed memory and vCPU values
- balloon enabled and disabled
- extra `virtiofs` and `9p` shares
- external Nix store virtiofs socket
- graphical mode for each supported backend
- `microvm.qemu.extraArgs` and `microvm.user` passthrough
- Linux and Darwin policy branches where practical

Repo-level checks should also cover:

- thinner manifest schema validation
- generated Nix manifest shape
- existing fake-tools E2E launch path
- migration compatibility for the current typed `qemu` manifest until it is
  intentionally removed

## Risks

- Policy drift from upstream `microvm.nix` if `virtie` silently recreates too
  much of its launch behavior.
- Under-specifying package or platform facts that Go cannot reliably infer from
  Nix store paths.
- Replacing `manifest.qemu` with a second unstable copy of `microvm` options
  instead of a stable launcher facts contract.
- Removing Nix fields before the parity suite proves that the generated QEMU
  invocation is behaviorally equivalent.

## Acceptance Criteria

- [ ] The generated manifest no longer embeds a fully resolved `qemu` section for
      the supported launch path.
- [ ] `virtie` derives the supported QEMU policy from the thinner manifest facts
      and produces the same effective QEMU invocation as the current path.
- [ ] Parity checks cover the required representative configurations.
- [ ] `agentspace-qemu-config.nix` is deleted and not replaced with an equivalent
      Nix policy builder elsewhere.
- [ ] Direct manifest producer migration notes are documented if compatibility
      changes.
- [ ] The existing `virtie launch --manifest=MANIFEST` CLI and supported
      sandbox UX remain unchanged.
