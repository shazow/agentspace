# Virtie Hotplug

Runtime device hotplug support for `virtie` through typed manifest entries.

**Status**: In-Progress

## Goals

Provide a first hotplug path for already-running `virtie` VMs without expanding
the agentspace Nix API yet.

- Define typed direct-manifest entries for `[[hotplug.virtiofs]]`,
  `[[hotplug.net]]`, and `[[hotplug.block]]`.
- Support `mounts[].hotplugged = true` as sugar for a virtiofs hotplug device.
- Automatically preallocate one QEMU PCIe root port for each lowered hotplug
  device so it can be attached after launch.
- Add `virtie hotplug --manifest=MANIFEST ID` and
  `virtie hotplug --manifest=MANIFEST --detach ID`.
- Persist per-device runtime state under `state_dir/hotplug/<id>.json`.
- Keep the relationship to
  [#112](https://github.com/shazow/agentspace/issues/112) visible: future
  native `mkSandbox` options should lower into these typed manifest fields.

Out of scope:

- Native Nix module or `mkSandbox` API for hotplugged shares in this branch.
- Arbitrary user-provided QMP, host exec, guest exec, or template variables in
  the public hotplug shape.
- Backend-neutral hotplug behavior beyond the current QEMU/QMP execution model.
- Full guest-side network and block configuration.

Acceptance criteria:

- [x] Manifest accepts `[[hotplug.virtiofs]]`, `[[hotplug.net]]`, and
  `[[hotplug.block]]`.
- [x] QEMU root-port allocation is automatic from the lowered hotplug count.
- [x] Manifest accepts `mounts[].hotplugged` and optional `mounts[].target`.
- [x] Hotplugged virtiofs mounts are excluded from launch-time QEMU devices and
  managed `run` processes.
- [x] Hotplugged virtiofs mounts lower to the same runtime device type as
  explicit `[[hotplug.virtiofs]]` entries.
- [x] QEMU launch emits stable `pcie-root-port` devices for allocated hotplug
  ports.
- [x] CLI supports attach and detach by hotplug id.
- [x] Virtiofs attach starts `virtiofsd`, waits for its socket, attaches QMP
  objects, optionally mounts in the guest, writes state, and rolls back on
  partial failure.
- [x] Virtiofs detach optionally unmounts in the guest, waits for
  `DEVICE_DELETED`, removes the chardev, stops the host process, and removes
  state/socket files.
- [x] Net attach/detach emits `netdev_add`, `device_add`, async `device_del`,
  and `netdev_del`.
- [x] Block attach/detach emits `blockdev-add`, `device_add`, async
  `device_del`, and `blockdev-del`.
- [ ] A future agentspace/mkSandbox API exposes hotplugged shares in the spirit
  of #112.

## Manifest Shape

```toml
[[hotplug.virtiofs]]
id = "cache"
source = "/tmp/cache"
target = "/mnt/cache" # optional
socket = "cache.sock" # optional, defaults to "<id>.sock"
bin = "virtiofsd"     # optional
args = []             # optional
```

```toml
[[hotplug.net]]
id = "vpn"
backend = "user" # v1 supports user only
mac = "02:02:00:00:00:10"
forward = [
  { proto = "tcp", host = "127.0.0.1:2223", guest = "10.0.2.15:22" },
]
```

```toml
[[hotplug.block]]
id = "data"
image = "data.qcow2"
format = "qcow2" # raw or qcow2
read_only = false
serial = "data"
```

The old arbitrary-QMP `[[hotplug]]` shape is intentionally removed before this
branch lands.

## Runtime Design

`virtie/internal/hotplug` owns the typed device descriptors, state records, QMP
command construction, rollback sequencing, and attach/detach orchestration.
`virtie/internal/manager` adapts that package to the existing process runner,
QMP dialer/client, guest-agent dialer, socket waiter, path resolution, and
stage-error wrapping.

Hotplug buses are assigned deterministically from the lowered runtime order:
explicit virtiofs, net, block, then virtiofs devices generated from
`mounts[].hotplugged = true`.

Net and block are intentionally minimal in v1. They attach and detach the QEMU
device only. Full functionality would require guest-side link naming,
DHCP/static address policy, route setup, block discovery, partition/filesystem
policy, and mount behavior.

## Relationship To #112

[#112](https://github.com/shazow/agentspace/issues/112) tracks moving features
currently consumed through `config.microvm.*` into native `mkSandbox` options.
Hotplugged shares should eventually follow that direction rather than becoming
another user-facing reason to understand `config.microvm.shares`.

The current branch intentionally stops at the virtie manifest/runtime layer:
hand-written or direct manifest producers can use hotplug now, and the future
agentspace API can lower into the same typed manifest fields.
