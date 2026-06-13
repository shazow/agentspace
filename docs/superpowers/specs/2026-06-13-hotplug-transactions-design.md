# Hotplug Transactions Design

## Purpose

Deepen virtie's hotplug module so attach and detach are transaction-level operations. The consumer commandline interface stays the same, including the control-socket-first behavior and direct-QMP fallback. Internal Go package interfaces may break where that improves locality and leverage.

## Current Friction

Hotplug behavior is split across shallow modules:

- `virtie/internal/manager/hotplug.go` assembles a `hotplug.Runtime` for direct fallback.
- `virtie/internal/manager/runtime/concrete_hotplug.go` assembles the same runtime for control-socket handling.
- `virtie/internal/hotplug/hotplug.go` owns most sequencing, but its QMP interface is raw JSON plus `DeviceDel`.
- Guest mount commands, QEMU device IDs, rollback order, state files, and process cleanup are visible across seams.

The result is low locality: changing hotplug QMP behavior, guest mount behavior, or adapter wiring requires reading manager, runtime, hotplug, and qmpclient code together.

## Design

Create a deeper hotplug transaction module. Callers ask the module to attach or detach a named manifest hotplug device. The implementation owns lookup, sequencing, state, rollback, host process handling, typed QMP operations, and guest mount intent.

The commandline surface remains unchanged:

- `virtie hotplug` still validates the manifest.
- It still tries the runtime control socket first.
- It still falls back to a direct QMP path when the socket is unavailable or unsupported.
- Attach and detach behavior, stage wrapping, and error presentation remain compatible.

Internal package interfaces can change:

- The exported shape of `hotplug.Runtime` can be replaced or reduced.
- `runtime.HotplugQMP` can disappear.
- The hotplug QMP seam should stop exposing raw JSON commands to callers.
- Tests can move from duplicated manager/runtime coverage toward transaction-level coverage.

## Module Shape

The hotplug transaction module exposes a small interface at the caller seam:

- Build a transaction runner from manifest hotplug devices, state dir, working dir, and concrete adapters.
- Attach one device by id.
- Detach one device by id.

The implementation hides:

- Duplicate id detection and unsupported kind errors.
- PCIe hotplug bus naming.
- State path calculation, state reads, writes, and cleanup.
- Virtiofsd process startup, socket waiting, rollback, and PID termination.
- QMP attach command construction and rollback.
- QMP `device_del` plus `DEVICE_DELETED` waiting.
- QMP post-delete cleanup.
- Guest mount and unmount command construction.

## Adapters

Manager direct fallback and runtime control handling should share the same adapter assembly path as much as practical.

Adapters at the seam:

- Host process starter: starts, stops, and signals virtiofsd process groups.
- Socket waiter: waits for host-side socket readiness.
- QMP device adapter: performs typed hotplug device operations against an existing QMP client.
- Guest mount adapter: runs guest mount and unmount intent through QGA.

The QMP adapter should be typed around hotplug concepts. Hotplug should not require callers or tests to pass arbitrary QMP JSON strings.

## Data Flow

Attach:

1. Validate and select the configured hotplug device by id.
2. Reject existing state for that id.
3. Start any host process needed by the device.
4. Attach QMP device state.
5. Apply guest mount intent when required.
6. Persist hotplug state.
7. Roll back completed steps on failure.

Detach:

1. Validate and read persisted state for that id.
2. Apply guest unmount intent when required.
3. Remove the QMP device and wait for deletion.
4. Run QMP post-delete cleanup.
5. Clean up host process/socket state when required.
6. Remove persisted state.

## Error Handling

Keep existing user-facing error behavior compatible:

- Manifest validation remains preflight.
- Control socket errors still fall back only when unavailable or unsupported.
- Hotplug transaction errors still flow through `launch.WrapHotplugError`.
- State mismatch errors remain explicit before cleanup starts.
- Rollback best-effort failures remain secondary unless the current code already returns them.

New internal errors should name the hotplug id and operation stage when that context would otherwise be lost.

## Testing

Primary tests should exercise the hotplug transaction module:

- Attach writes state after host, QMP, and guest steps.
- Attach rolls back host and QMP on failure.
- Detach waits for QMP device deletion before post-delete cleanup.
- Detach rejects state kind mismatches before guest, QMP, or host cleanup.
- Net and block attach/detach use typed QMP behavior without asserting raw JSON at the caller seam.
- Duplicate ids and unsupported kinds remain covered.

Manager and runtime tests should shrink to seam behavior:

- Manager uses control socket first.
- Manager falls back to direct QMP only for unavailable or unsupported control socket errors.
- Runtime control dispatches attach and detach to the shared transaction module.

## Out Of Scope

- Changing the `virtie hotplug` commandline interface.
- Changing manifest syntax.
- Adding new hotplug device kinds.
- Implementing full guest-side network setup.
- Implementing full guest-side block discovery or mount policy.
- Reworking the entire `qmpclient` package beyond what hotplug transactions need.

## Migration Notes

This is intentionally compatibility-breaking for internal Go package interfaces. No `MIGRATION.md` entry is required unless an exported consumer-facing surface changes. If the refactor changes package-level behavior visible to external Go consumers, document that separately.
