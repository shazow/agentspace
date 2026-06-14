# Virtie Manager Refactor Architecture Record

**Status:** Complete

The manager refactor split `virtie/internal/manager` around a launch-owned
runtime and a typed Unix-socket control plane. The current design keeps QMP,
QGA, process ownership, suspend state, runtime state, and teardown inside the
launch process, while other `virtie` commands talk to that process through
`virtie.sock` when available.

## Goals Delivered

- `LaunchWithOptions` composes planning, concrete manager startup, foreground
  wait, and cleanup through one CLI-facing facade.
- The launch process starts a typed `virtie.sock` server after QMP readiness
  and closes it during runtime teardown.
- `virtie hotplug`, status, info, and balloon control use typed control
  client/server calls. `virtie suspend` uses the control socket when available
  and keeps the PID/signal path for existing launch-process compatibility.
- QMP-affecting runtime operations go through the launch-owned runtime and a
  serialized QMP client.
- Direct-QMP hotplug and build-tag capability fallbacks have been removed.
- Manager tests cover the typed transport, socket permissions, status,
  suspend, hotplug, balloon, info, startup ordering, and cleanup behavior.

## Core Invariants

- Only the launch-owned runtime may perform in-process QMP-affecting lifecycle
  operations after QMP connects.
- `qmpclient.Serialized` wraps QMP access so suspend, hotplug, balloon,
  restore, save, and shutdown command sequences do not interleave unsafely.
- Suspend is a lifecycle event, not a standalone RPC action. Local signals and
  control-socket requests share `launch.Lifecycle` and
  `launch.SuspendCoordinator`.
- Normal close, startup failure cleanup, saved-suspend exit, control shutdown,
  process teardown, QMP disconnect, socket cleanup, lock cleanup, and stats
  finalization must remain idempotent and ordered.
- Control errors use typed `manager/control` error codes. Compatibility
  decisions should inspect those codes instead of string-matching messages.

## Package Map

### `virtie/internal/manager`

- CLI-facing facade: `Launch`, `LaunchWithOptions`, `Launcher`, `Config`,
  `DefaultConfig`, `NewLauncher`, `ResumeMode`, `LaunchOptions`, and
  `WaitMode`.
- Owns concrete environment wiring: file locks, VSock CID checks, process
  runner, socket waiter, QMP/QGA dialers, signal channel, notification
  commands, logging, timeouts, and output writers.
- Owns concrete startup ordering and adapts manifest data plus concrete
  dependencies into `manager/launch`, `manager/runtime`, `manager/control`,
  `qmpclient`, `qga`, hotplug, balloon, and SSH helpers.
- Keeps the PID/signal suspend compatibility path until a separate policy
  decision removes it.

### `virtie/internal/manager/launch`

- Owns value-oriented launch inputs and resolved runtime data:
  `Spec`, `Options`, `ResumeMode`, `WaitMode`, `RuntimePaths`,
  `SuspendState`, and `Plan`.
- Owns pre-runtime planning: resume resolution, CID acquisition, filesystem
  preparation, QEMU command finalization, runtime lock/PID setup, and
  plan-owned socket cleanup.
- Owns lifecycle coordination: `Lifecycle`, `SuspendCoordinator`,
  queued-suspend handling, signal mapping, info requests, and foreground
  process/lifecycle waiting.
- `Stats`, `TimerEvent`, and `ProcessSet` live with launch startup because
  readiness timing and process ownership are startup concerns.
- Owns concrete readiness helpers for sockets, QMP, QGA, SSH readiness,
  virtiofs waits, and unexpected process exits.
- Owns focused launch helpers: SSH command construction, SSH foreground
  retry/autoprovisioning, guest file provisioning/write-back, workspace CWD
  mounting, runtime state files, stats, readiness waits, and suspend/resume
  notifications. Manager owns the concrete launch-process startup ordering.

### `virtie/internal/manager/runtime`

- Owns the long-lived `Core` object for QMP, launch-owned process and stats
  references, control server, suspend queue, foreground wait, close callbacks,
  runtime state, and typed control methods.
- `RuntimeConfig` constructs a usable runtime with the required plan,
  processes, foreground wait, close callbacks, QMP client, suspend
  coordinator, logger, timeouts, and info collector. Startup lifecycle actions
  such as `SetReady` and `StartControl` remain explicit.
- `State` provides consistent status output; launch stats are mapped into
  control responses from `manager/launch`.
- `Closer`, `CloseActions`, and `ShutdownResources` keep already-started
  runtime teardown ordering idempotent and testable.
- Concrete control methods map runtime state, info, suspend, and balloon
  behavior into typed `manager/control` responses. Hotplug is registered from
  the manager/control periphery so the feature remains removable.

### `virtie/internal/manager/control`

- Defines the typed JSON-over-Unix-socket protocol: request/response structs,
  `RuntimeState`, `RuntimeStats`, `StatusPaths`, `RPCError`, and `ErrorCode`.
- Provides capability interfaces for core status/info, suspend, hotplug, and
  balloon behavior.
- Implements typed router, server, listener, dialer, and client calls.
- Centralizes typed protocol errors plus the failed-precondition and
  socket-unavailable helpers used by control clients.

### `virtie/internal/qmpclient`

- Owns QMP protocol access, socket monitor dialing, retry dialing, serialized
  access, raw command helpers, migration polling, restore, and suspend save.
- `Serialized` is idempotent; passing an already serialized client keeps the
  same wrapper.

### `virtie/internal/qga`

- Owns QGA protocol access, socket dialing, retry dialing, ping readiness,
  file transfer primitives, guest-exec polling, output decoding, and guest
  process-list parsing/formatting.

## Current Launch Flow

1. `manager` builds a `launch.Plan` from manifest, options, resume state, and
   notification policy.
2. `manager.startWithPlan` creates launch stats, lifecycle, runtime lock,
   process set, CID/QEMU command data, and prepared filesystem/socket state.
3. `manager.startWithPlan` starts run commands, waits for virtiofs sockets,
   marks boot start, starts QEMU, waits for QMP, serializes QMP, installs QMP
   shutdown, and restores saved VM state when present.
4. `manager.startWithPlan` builds suspend handling, foreground wait, and a
   `runtime.Core` from QMP, process ownership, launch stats, close callbacks,
   and control handlers.
5. `manager.startWithPlan` marks runtime state ready, starts the control server,
   drains queued suspend, provisions fresh guest files, observes SSH readiness,
   and enables write-back-on-exit.
6. Foreground wait starts the balloon controller task when configured, then
   either runs the SSH foreground session or prints an SSH hint and waits for
   the VM.
7. Runtime close performs write-back when enabled, control shutdown, process
   teardown, QMP disconnect, socket and lock cleanup, and stats finalization.

## Cleanup Policy

The safe-abstraction cleanup, follow-up simplification cleanup, direct-QMP
hotplug fallback removal, and partial launcher entrypoint cleanup have landed.
The active follow-up item is intentionally narrow:

- Decide when the PID/signal suspend compatibility path can be removed.
- Keep this record current when lifecycle topology, control-socket behavior,
  QMP ownership, or fallback policy changes.
