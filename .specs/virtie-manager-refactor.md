# Virtie Manager Refactor Architecture Record

**Status:** Complete

The manager refactor split `virtie/internal/manager` around a launch-owned
runtime and a typed Unix-socket control plane. The current design keeps QMP,
QGA, process ownership, suspend state, runtime state, and teardown inside the
launch process, while other `virtie` commands talk to that process through
`virtie.sock` when available.

## Goals Delivered

- `LaunchWithOptions` composes planning, launch starter startup, foreground
  wait, and cleanup instead of owning lifecycle startup inline.
- The launch process starts a typed `virtie.sock` server after QMP readiness
  and closes it during runtime teardown.
- `virtie suspend`, `virtie hotplug`, status, info, and balloon control use
  typed control client/server calls where supported.
- QMP-affecting runtime operations go through the launch-owned runtime and a
  serialized QMP client.
- CLI compatibility fallbacks remain for unavailable control sockets,
  unsupported build-tag capabilities, and migration-era launch processes.
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
- Adapts manifest data and concrete dependencies into `manager/launch`,
  `manager/runtime`, `manager/control`, `qmpclient`, `qga`, hotplug, balloon,
  and SSH helpers.
- Keeps compatibility fallbacks for suspend and hotplug until a separate policy
  decision removes them.

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
- `Starter`, `Host`, and `Runtime` own planned-launch startup ordering,
  startup-failure cleanup, launch stats, and the seam between concrete host
  effects and runtime construction.
- `Stats`, `TimerEvent`, and `ProcessSet` live with launch startup because
  readiness timing and process ownership are startup concerns.
- Owns concrete readiness helpers for sockets, QMP, QGA, SSH readiness,
  virtiofs waits, and unexpected process exits.
- Owns focused launch helpers: run command startup, QEMU process startup,
  QMP shutdown hook finalization, SSH command construction, SSH foreground
  retry/autoprovisioning, guest file provisioning/write-back, workspace CWD
  mounting, restore/save orchestration, and suspend/resume notifications.

### `virtie/internal/manager/runtime`

- Owns the long-lived `Core` object for QMP, launch-owned process and stats
  references, control server, suspend queue, foreground wait, close hooks,
  runtime state, and typed control methods.
- `RuntimeConfig` constructs a usable runtime with the required plan,
  processes, foreground wait, close hooks, QMP client, suspend coordinator, and
  dependencies. Startup lifecycle actions such as `SetReady` and
  `StartControl` remain explicit.
- `Task` and `TaskGroup` own foreground task cancellation and ordered shutdown.
- `State` provides consistent status output; launch stats are mapped into
  control responses from `manager/launch`.
- `Closer`, `CloseActions`, `StartupFailureActions`, and
  `ShutdownResources` keep teardown ordering idempotent and testable.
- Concrete control methods map runtime state, info, suspend, and balloon
  behavior into typed `manager/control` responses. Hotplug is registered from
  the manager/control periphery so the feature remains removable.

### `virtie/internal/manager/control`

- Defines the typed JSON-over-Unix-socket protocol: request/response structs,
  `RuntimeState`, `RuntimeStats`, `StatusPaths`, `RPCError`, and `ErrorCode`.
- Provides capability interfaces for core status/info, suspend, hotplug, and
  balloon behavior.
- Implements typed router, server, listener, dialer, and client calls.
- Centralizes unsupported, failed-precondition, and socket-unavailable helpers
  used by compatibility fallbacks.

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
2. `manager.startWithPlan` constructs `launch.Starter` with concrete
   `launchHost` and `launchRuntime` providers.
3. `launch.Starter` creates launch stats, lifecycle, runtime lock, process set,
   CID/QEMU command data, and prepared filesystem/socket state.
4. `launch.Starter` starts run commands, waits for virtiofs sockets, marks boot
   start, starts QEMU, waits for QMP, serializes QMP, installs QMP shutdown,
   and restores saved VM state when present.
5. `launch.Starter` asks `launchRuntime` for suspend handling, foreground wait,
   and a `runtime.Core` built from QMP, process ownership, launch stats, close
   hooks, and control handlers.
6. `launch.Starter` marks runtime state ready, starts the control server,
   drains queued suspend, provisions fresh guest files, observes SSH readiness,
   and enables write-back-on-exit.
7. Foreground wait starts the balloon controller task when configured, then
   either runs the SSH foreground session or prints an SSH hint and waits for
   the VM.
8. Runtime close performs write-back when enabled, control shutdown, process
   teardown, QMP disconnect, socket and lock cleanup, and stats finalization.

## Cleanup Policy

The safe-abstraction cleanup and follow-up simplification cleanup have landed.
The active follow-up items are intentionally narrow:

- Decide when compatibility fallbacks for direct-QMP hotplug and PID/signal
  suspend can be removed.
- Reconsider whether `Launcher.Plan` and `Launcher.Start` should remain as
  internal partial-lifecycle entrypoints or become private test helpers.
- Keep this record current when lifecycle topology, control-socket behavior,
  QMP ownership, or fallback policy changes.
