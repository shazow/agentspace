# Tiny Sandbox Experiment

`lib.mkTinySandbox` is an experimental constructor for a Nix-built, initrd-only
appliance VM. It is separate from `lib.mkSandbox` and does not aim to provide a
full NixOS agentspace.

**Status**: Experimental

The first success target is intentionally narrow:

- boot a Linux kernel with a Nix-built initrd
- start the QEMU guest agent inside the initrd
- start OpenSSH inside the initrd
- listen on guest vsock port 22
- mount the host workspace through a managed virtiofs share
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
- `mountWorkspace`
- `workspaceMountPoint`
- `writeFiles`

## Current Contract

The tiny profile is still an initrd appliance, but now includes the host control
and workspace pieces needed for normal `virtie` operation:

- QEMU guest agent is always enabled at `qga.sock`
- `writeFiles` is supported through the guest agent during fresh launch
- `mountWorkspace = true` by default
- the current host directory is shared as one managed `virtiofs` share tagged
  `workspace`
- the workspace is mounted in the initrd at `workspaceMountPoint`, defaulting to
  `/home/${user}/workspace`
- no block devices or microvm volumes
- `microvm.guest.enable = false`
- `microvm.storeOnDisk = false`
- `microvm.writableStoreOverlay = null`

Nix store sharing, persistent storage, generic volumes, and the normal Home
Manager/NixOS user environment are not part of the tiny appliance contract. The
initrd creates only the minimal passwd/group/home/ssh state needed for SSH and
the workspace mount.

OpenSSH is used for the first experiment because it matches the existing host
SSH flow. Dropbear is the expected future size reduction path, but that would
change the guest daemon implementation and should be evaluated separately.

## Benchmark Reference

Use `nix run .#tiny-sandbox-benchmark` to compare the size and boot-to-SSH
impact of the guest agent, `writeFiles`, and workspace virtiofs. The benchmark
builds two flake refs, records toplevel/initrd size, and, when
`/dev/vhost-vsock` is visible, launches each VM and captures `virtie`'s
`boot_to_ssh` stats.

Example command:

```sh
nix run .#tiny-sandbox-benchmark -- \
  --iterations 1 \
  --out /tmp/tiny-sandbox-benchmark.tsv \
  "git+file://$PWD?rev=$(git rev-parse HEAD)" \
  "path:$PWD"
```

Reference run from 2026-05-05 on the NixOS QEMU workspace VM, comparing Git
HEAD before these features with the working tree after enabling QGA,
`writeFiles`, and workspace virtiofs, plus a full `mkSandbox` baseline from
the same working tree using 4096 MiB of guest memory:

| profile | metric | value |
| --- | --- | ---: |
| tiny before | toplevel closure bytes | 1195996240 |
| tiny before | initrd closure bytes | 14910520 |
| tiny before | initrd file bytes | 14910037 |
| tiny after | toplevel closure bytes | 1197126648 |
| tiny after | initrd closure bytes | 16040936 |
| tiny after | initrd file bytes | 16040452 |
| full | toplevel closure bytes | 1349995872 |
| full | initrd closure bytes | 23687680 |
| full | initrd file bytes | 23687194 |
| tiny before run 1 | wall elapsed ms | 2304 |
| tiny before run 1 | virtie stats | `started_to_boot=368.799µs boot_to_ssh=2.126174958s ssh_to_completed=121.236174ms total=2.247779931s` |
| tiny after run 1 | wall elapsed ms | 2888 |
| tiny after run 1 | virtie stats | `started_to_boot=101.7346ms boot_to_ssh=2.603010513s ssh_to_completed=129.517161ms total=2.834262274s` |
| full run 1 | wall elapsed ms | 14975 |
| full run 1 | virtie stats | `started_to_boot=142.520713ms boot_to_ssh=12.033426302s ssh_to_completed=2.746541059s total=14.922488074s` |

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
- [x] Enable the QEMU guest agent for VM control and `writeFiles`.
- [x] Support managed workspace virtiofs by default.
- [x] Support `mountWorkspace`, `workspaceMountPoint`, and `writeFiles` where
  they match `mkSandbox`.
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

- **Nix store sharing or writable Nix store overlays**: pulls tiny mode toward a
  normal NixOS runtime and reintroduces block devices or `virtiofs` shares.
- **Persistent home images or arbitrary microvm volumes**: conflicts with the
  initrd-only contract and makes lifecycle, formatting, and suspend/resume
  behavior indistinguishable from `mkSandbox`.
- **Home Manager modules and full user package environments**: require a real
  root filesystem and system activation model. Tiny mode should expose a small
  appliance userland instead.
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
