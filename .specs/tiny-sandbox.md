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
- `ssh.retryDelayMs`
- `ssh.hostKeyPath`
- `ssh.useDeterministicTestingHostKey`
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
- no QEMU user network devices
- no x86 i8042 controller device
- `microvm.guest.enable = false`
- `microvm.storeOnDisk = false`
- `microvm.writableStoreOverlay = null`

Nix store sharing, persistent storage, generic volumes, and the normal Home
Manager/NixOS user environment are not part of the tiny appliance contract. The
initrd creates only the minimal passwd/group/home/ssh state needed for SSH and
the workspace mount. By default the initrd generates one ephemeral ed25519 SSH
host key at boot. `ssh.hostKeyPath` can copy a caller-provided ed25519 private
host key into the initrd, and `ssh.useDeterministicTestingHostKey = true` uses a
fixed test-only key for checks and benchmarks. Both build-time host-key paths
place private key material in the Nix store and should not be used for
production secrets.

OpenSSH is used for the first experiment because it matches the existing host
SSH flow. Dropbear is the expected future size reduction path, but that would
change the guest daemon implementation and should be evaluated separately.

## Size Reduction

Tiny mode trims NixOS defaults that are not used by the initrd appliance:

- in-guest Nix and generated Nix/NixOS documentation are disabled
- default system/user packages and command-not-found are disabled
- dbus, dhcpcd, logrotate, lvm, sudo, and udev are disabled
- default initrd kernel modules are disabled; tiny mode keeps only its explicit
  virtio, virtiofs, fuse, and vsock module set
- the scripted-initrd ext filesystem module is disabled because tiny mode has
  no block devices, ext root, or persistent ext volumes
- runtime SSH host-key generation is ed25519-only, and `ssh-keygen` is omitted
  from the initrd when a build-time host key is configured
- the OpenSSH listener starts before QEMU guest-agent probing, after the
  workspace mount and host-key setup
- tiny mode emits no QEMU user network device and disables the x86 i8042 device

Reference planning measurements from 2026-05-05 showed the safe trim reducing
the tiny toplevel closure from about 1.20 GB to about 578 MB. Disabling default
initrd modules and the unused ext filesystem module reduced the packed initrd
from about 16.0 MB to about 13.7 MB.

Two more reductions are intentionally deferred. Importing NixOS
`profiles/perlless.nix` is not currently drop-in because the scripted initrd
and toplevel activation still pull Perl. Disabling the NixOS security wrappers
module saved only about 8.7 MB locally and requires a compatibility shim, so it
should be evaluated separately if further toplevel closure reduction matters.

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

Reference run from 2026-05-05 on the NixOS QEMU workspace VM, comparing commit
`a7fdd6581947` with the working tree after adding deterministic benchmark host
keys, ed25519-only runtime host-key generation, early SSH listener startup,
zero tiny user-network devices, and detailed launch stats. The tiny after paths
used `ssh.useDeterministicTestingHostKey = true`, `ssh.retryDelayMs = 100`,
one `writeFiles` entry, and workspace virtiofs. The full baseline used
`mkSandbox` from the same working tree with 4096 MiB of guest memory.

| profile | metric | value |
| --- | --- | ---: |
| tiny before | toplevel closure bytes | 568842504 |
| tiny before | initrd closure bytes | 13739176 |
| tiny before | initrd file bytes | 13738689 |
| tiny after | toplevel closure bytes | 568813552 |
| tiny after | initrd closure bytes | 13710232 |
| tiny after | initrd file bytes | 13709751 |
| full | toplevel closure bytes | 1349995872 |
| full | initrd closure bytes | 23687680 |
| full | initrd file bytes | 23687194 |
| tiny before | average wall elapsed ms, 5 runs | 5631.8 |
| tiny after | average wall elapsed ms, 5 runs | 4573.8 |
| full | average wall elapsed ms, 5 runs | 27040.2 |
| tiny before run 1 | virtie stats | `started_to_boot=102.964908ms boot_to_ssh=5.24109837s ssh_to_completed=222.63088ms total=5.566694158s` |
| tiny after run 1 | virtie stats | `started_to_boot=102.920936ms boot_to_qmp=233.294478ms qmp_to_guest_agent=3.704300393s guest_agent_to_files=106.803065ms files_to_first_ssh=4.449µs files_to_ssh=4.449µs boot_to_ssh=4.044402385s ssh_to_completed=232.626231ms total=4.379949552s ssh_attempts=1` |
| full run 1 | virtie stats | `started_to_boot=175.115765ms boot_to_qmp=245.265157ms qmp_to_guest_agent=20.726333285s guest_agent_to_files=240.790769ms files_to_first_ssh=6.221µs files_to_ssh=6.221µs boot_to_ssh=21.212395432s ssh_to_completed=5.467873424s total=26.855384621s ssh_attempts=1` |

The 5-run average wall elapsed time improved by about 1058 ms, from 5631.8 ms
to 4573.8 ms. The benchmark did not isolate individual changes, so the
following attribution is speculative and overlapping:

| optimization | estimated contribution | share of net improvement | rationale |
| --- | ---: | ---: | --- |
| Build-time deterministic host key plus removing runtime RSA host-key generation | 800-950 ms | 75-90% | Runtime OpenSSH RSA key generation was the largest obvious serial initrd cost. The after run skips `ssh-keygen` entirely for the benchmark key path. |
| Removing tiny QEMU user networking and disabling i8042 | 80-180 ms | 8-17% | Fewer devices reduce QEMU setup and kernel PCI/device probing. The effect is likely real but smaller than host-key generation. |
| Smaller initrd from omitting `ssh-keygen` in the build-time key path | 20-60 ms | 2-6% | The packed initrd shrank by about 29 KB in the default after path. This should help slightly, but overlaps with the host-key change. |
| Starting SSH before QGA probing | 0-30 ms in this benchmark | 0-3% | This benchmark uses `writeFiles`, so host-side SSH still waits for QGA file injection before connecting. It should matter more for tiny launches without `writeFiles`. |
| `ssh.retryDelayMs = 100` benchmark setting | 0 ms measured | 0% | All after runs reported `ssh_attempts=1`, so no retry delay was paid. |
| Detailed launch stats | approximately 0 ms | approximately 0% | This is instrumentation and should not materially affect boot time. |

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
- [x] Make host-key behavior configurable for checks and benchmark paths while
  keeping runtime-generated ephemeral host keys as the consumer default.
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
