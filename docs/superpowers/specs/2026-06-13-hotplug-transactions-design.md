# Hotplug Transactions Design

## Purpose

Deepen virtie's hotplug module so attach and detach are transaction-level operations, while keeping hotplug isolated enough to remove or replace if a future VM runtime does not support it. The consumer commandline interface stays the same, including the control-socket-first behavior and direct-QMP fallback. Internal Go package interfaces may break where that improves locality, leverage, and feature isolation.

## Current Friction

Before this work, hotplug behavior was split across shallow modules:

- Direct fallback and control-socket handling assembled equivalent runners in separate places.
- The hotplug QMP seam exposed low-level command details to callers.
- Guest mount commands, QEMU device IDs, rollback order, state files, and process cleanup were visible across seams.

The implemented shape keeps the direct fallback and control handler on one manager-side assembly path, keeps runtime core unaware of hotplug, and keeps QMP command construction inside the hotplug package's typed adapter.

## Design

Create a deeper hotplug transaction module at the feature periphery. Callers ask the module to attach or detach a named manifest hotplug device. The implementation owns lookup, sequencing, state, rollback, host process handling, typed QMP operations, and guest mount intent.

The compromise is that shared intermediary modules should not care about hotplugging. Runtime, control, process management, QMP transport, and guest-command execution should expose generic capabilities. Hotplug-specific policy composes those capabilities only at the edge where the `virtie hotplug` command and optional runtime control handler live.

The commandline surface remains unchanged:

- `virtie hotplug` still validates the manifest.
- It still tries the runtime control socket first.
- It still falls back to a direct QMP path when the socket is unavailable or unsupported.
- Attach and detach behavior, stage wrapping, and error presentation remain compatible.

Internal package interfaces changed to support that shape:

- The exported hotplug transaction runner shape was reduced to `hotplug.Runner`.
- `runtime.HotplugQMP` disappeared.
- The hotplug QMP seam no longer exposes raw JSON commands to callers.
- `runtime.Dependencies` stopped carrying hotplug-named dependencies.
- Core control routing avoids hard-coded hotplug handling through explicit handler registration.
- Tests moved from duplicated manager/runtime coverage toward transaction-level coverage.

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

## Feature Isolation

Hotplug should be removable with limited edits:

- Remove the hotplug command handler.
- Remove the optional hotplug control handler registration.
- Remove the hotplug transaction module and its tests.
- Remove manifest hotplug lowering and validation only if the consumer contract changes later.

Shared modules should remain useful without hotplug:

- Runtime core manages process lifecycle, QMP lifecycle, suspend, stats, and foreground behavior.
- Control core routes requests to registered handlers or reports unsupported operations.
- QMP client remains a monitor transport and common VM lifecycle adapter.
- Guest command execution remains a generic capability, not a hotplug-specific interface.

This means hotplug-specific names should stay out of central runtime dependency structs. A launch can assemble a hotplug handler beside the runtime rather than embedding hotplug methods and dependencies inside the runtime core.

## Adapters

Manager direct fallback and runtime control handling should share the same hotplug feature assembly path as much as practical.

Adapters at the seam:

- Host process adapter: starts, stops, and signals process groups using generic process facilities.
- Socket readiness adapter: waits for host-side socket readiness using generic socket waiting.
- QMP transport adapter: wraps an existing QMP client with hotplug-typed device operations inside the hotplug module.
- Guest command adapter: runs guest commands through QGA without exposing hotplug-specific names to shared modules.

The QMP adapter should be typed around hotplug concepts inside the hotplug module. Hotplug should not require callers or tests to pass arbitrary QMP JSON strings, and `qmpclient` should not gain broad hotplug policy unless another module also needs it.

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
- Runtime launch registers a hotplug handler only at the feature periphery.
- Control reports unsupported hotplug operations when no hotplug handler is registered.

Isolation tests should guard the compromise:

- Runtime dependency structs do not expose hotplug-named fields.
- Hotplug transaction tests do not require manager or runtime types.
- QMP client tests do not need hotplug device concepts.

## Out Of Scope

- Changing the `virtie hotplug` commandline interface.
- Changing manifest syntax.
- Adding new hotplug device kinds.
- Implementing full guest-side network setup.
- Implementing full guest-side block discovery or mount policy.
- Making `qmpclient` a hotplug-aware module.
- Embedding hotplug feature policy in runtime core.

## Migration Notes

This is intentionally compatibility-breaking for internal Go package interfaces. No `MIGRATION.md` entry is required unless an exported consumer-facing surface changes. If the refactor changes package-level behavior visible to external Go consumers, document that separately.
