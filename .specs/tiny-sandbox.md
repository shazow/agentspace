# Tiny Sandbox Experiment

`lib.mkTinySandbox` is an experimental constructor for a Nix-built, initrd-only
appliance VM. It is separate from `lib.mkSandbox` and does not aim to provide a
full NixOS agentspace.

**Status**: Experimental

The first success target is intentionally narrow:

- boot a Linux kernel with a Nix-built initrd
- start OpenSSH inside the initrd
- listen on guest vsock port 22
- attach through the existing `virtie launch --ssh` flow as `agent@vsock/<cid>`

Tiny and full sandbox examples should use the same SSH-facing and machine
sizing options where practical:

- `user`
- `hostName`
- `ssh.authorizedKeys`
- `ssh.identityFile`
- `ssh.autoconnect`
- `machine.memory` in MiB
- `machine.vcpu`, where `null` lets virtie choose the host-visible CPU count at
  launch time

## Current Contract

The tiny profile avoids persistent and shared storage:

- no virtiofs shares or virtiofsd daemons
- no block devices or microvm volumes
- `microvm.guest.enable = false`
- `microvm.storeOnDisk = false`
- `microvm.writableStoreOverlay = null`
- no QEMU guest agent

This means `writeFiles`, workspace mounts, Nix store sharing, and the normal
Home Manager/NixOS user environment are not part of the tiny appliance contract.
The initrd creates only the minimal passwd/group/home/ssh state needed for SSH.

OpenSSH is used for the first experiment because it matches the existing host
SSH flow. Dropbear is the expected future size reduction path, but that would
change the guest daemon implementation and should be evaluated separately.

## Parity TODOs

These items would make `mkTinySandbox` easier to use with examples that also
work for `mkSandbox`, without turning the tiny appliance into a full agentspace.

- [x] Share SSH-facing option names with `mkSandbox`: `user`, `hostName`,
  `ssh.authorizedKeys`, `ssh.identityFile`, `ssh.command`, and
  `ssh.autoconnect`.
- [x] Share machine sizing option names with `mkSandbox`: `machine.memory` and
  `machine.vcpu`.
- [x] Support runtime CPU defaulting by omitting `qemu.smp.cpus` when
  `machine.vcpu = null`.
- [ ] Keep the minimal example identical except for constructor name. The target
  example should be valid for both `lib.mkSandbox` and `lib.mkTinySandbox`:

  ```nix
  {
    hostName = "agent-example";
    user = "agent";
    machine.memory = 256;
    machine.vcpu = null;
    ssh.authorizedKeys = [ "ssh-ed25519 ..." ];
    ssh.identityFile = "./id_ed25519";
  }
  ```

- [ ] Add a consumer-workflow check that instantiates both constructors from the
  same minimal attrset and asserts the shared fields produce equivalent manifest
  values.
- [ ] Decide whether `ssh.command` should be documented as fully supported for
  tiny mode. It works through the shared launcher, but tiny mode lacks the
  normal shell environment and should advertise only simple commands until the
  initrd userland is expanded.
- [ ] Add a tiny-specific `writeTextFile` or `authorizedFiles` style option only
  if a concrete need appears. Do not copy `writeFiles`; tiny has no guest agent.
- [ ] Make host-key behavior configurable if repeated SSH host identity becomes
  important for consumers. The current ephemeral host keys are correct for an
  appliance smoke path but are not parity with a persistent NixOS guest.
- [ ] Investigate replacing `socat + sshd -i` with an inetd-style SSH server
  setup that has clearer lifecycle and logging. This should preserve the
  existing host-side SSH argv and `virtie launch --ssh` behavior.
- [ ] Evaluate Dropbear as an optional implementation after OpenSSH behavior is
  stable. The goal would be smaller closure and simpler initrd serving, not a
  user-visible SSH interface change.
- [ ] Add a manual host-capability check target or script for the real vsock e2e
  path. The Nix derivation can only skip when `/dev/vhost-vsock` is hidden by
  build sandboxing.

## Out of Scope

These features should stay out of tiny mode unless the goal changes from
"initrd appliance" to "small full agentspace".

- **Workspace mounts through virtiofs**: requires host `virtiofsd`, QEMU shared
  memory, guest mount orchestration, and a writable user environment. This is
  the core full-sandbox storage model, not an initrd appliance concern.
- **Nix store sharing or writable Nix store overlays**: pulls tiny mode toward a
  normal NixOS runtime and reintroduces block devices or `virtiofs` shares.
- **Persistent home images or arbitrary microvm volumes**: conflicts with the
  initrd-only contract and makes lifecycle, formatting, and suspend/resume
  behavior indistinguishable from `mkSandbox`.
- **Home Manager modules and full user package environments**: require a real
  root filesystem and system activation model. Tiny mode should expose a small
  appliance userland instead.
- **QEMU guest agent and `writeFiles` parity**: the guest agent is a long-lived
  service expected in the full guest. Tiny mode should avoid adding it solely to
  support file injection.
- **Swap files**: require writable guest storage and add little value to an
  appliance intended to boot fast with a small, explicit memory budget.
- **Runtime balloon control**: useful for the full sandbox where memory pressure
  changes over long sessions. Tiny mode should stay fixed-size unless a real
  appliance workload needs dynamic memory.
- **Suspend/resume state for tiny guests**: QEMU migration state would dominate
  the simplicity of an ephemeral initrd VM. Restarting should remain the normal
  recovery path.
- **macOS support**: current tiny mode depends on Linux vsock and QEMU behavior.
  Support should not be promised until there is a tested non-Linux transport.
- **Alternate hypervisors**: the current `virtie` path is QEMU-specific and the
  tiny appliance depends on the QEMU vsock device shape.
- **General initrd customization hooks**: broad hooks would make tiny mode a
  second module system. Prefer adding narrowly scoped appliance options when
  concrete use cases appear.

macOS support is not a goal for this mode yet. The path depends on Linux-first
vsock and initrd appliance behavior.
