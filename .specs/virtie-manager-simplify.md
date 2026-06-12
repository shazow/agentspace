# Virtie Manager Simplification Cleanup

**Status:** Implemented

This cleanup followed the adversarial review in the previous version of this
file. It consolidated framework-shaped glue introduced during the manager
refactor while preserving the real boundaries: `manager/control`,
`manager/launch`, `manager/runtime`, `qmpclient`, and `qga`.

## Implemented

- Removed manager-local QMP migration leftovers. QMP delimiter and timeout
  helpers now live only in `qmpclient`.
- Collapsed duplicate cleanup path state by removing `RuntimePaths.Cleanup`;
  `Plan.CleanupFiles` is the plan-owned source for additional socket cleanup.
- Flattened socket waiting. `WaitForSockets` is now the direct socket wait loop
  with default stage wrapping and watcher checks; the generic `AsyncWait`
  callback operation is gone.
- Flattened foreground lifecycle waiting. `WaitForLifecycleProcess` is now the
  direct process/delay/suspend/info/cancel loop; `EventWait` and `ProcessWait`
  are gone.
- Simplified QMP, QGA, and SSH-readiness waits. They no longer accept custom
  check/wrap/cancel callbacks and instead use the package defaults.
- Inlined runtime startup sequencing into `manager.startLaunchRuntime`.
  The `RuntimeStartup`, `RuntimeStartupResult`, and
  `StartRuntimeProcesses` callback bag is gone. `StartRuns`, `StartQEMU`, and
  QMP shutdown finalization remain focused helpers.
- Moved facade wiring out of `launch.Config`. `manager.Config` now owns
  concrete dependency injection, defaults, logging, signals, dialers, timeouts,
  PID signaling, and notifier configuration. The launch package keeps only the
  small dependency interfaces it consumes.
- Made runtime construction more honest. `RuntimeConfig` now includes the
  launch plan, process set, shutdown delay, foreground wait callback, and close
  hooks. The post-construction `SetProcesses`, `SetForegroundWait`, and
  `SetCloseHooks` setters are gone.
- Made QMP serialization idempotent so a serialized client can be passed
  through runtime construction without creating nested locks.
- Consolidated common teardown resource fields into
  `runtime.ShutdownResources`, shared by normal close and startup-failure
  cleanup actions.
- Replaced the old refactor specification with a concise architecture record
  in `.specs/virtie-manager-refactor.md`.

## Deliberately Kept

- `manager/control`, `qmpclient`, and `qga` remain real protocol boundaries.
- `launch.Plan`, resolved paths, resume state, lock/PID state, filesystem
  preflight, and socket cleanup remain value-oriented launch data.
- `Lifecycle` and `SuspendCoordinator` remain the shared path for local
  signals and RPC suspend requests.
- `runtime.Runtime`, `ProcessSet`, `Task`, `TaskGroup`, `State`, `Stats`,
  QMP serialization, and idempotent close remain core safety boundaries.
- Guest-file provisioning/write-back, suspend metadata, SSH retry and
  autoprovisioning, hotplug adapters, and balloon adapters remain outside
  `manager.go`.

## Deferred Decisions

- Compatibility fallback removal is a policy decision, not a cleanup-only
  task. PID/signal suspend and direct-QMP hotplug should remain until mixed
  old/new launch behavior and unsupported build-tag behavior are explicitly
  retired.
- `Launcher.Plan` and `Launcher.Start` are still available as internal
  partial-lifecycle entrypoints. They should be removed or made private only
  after deciding that no future integration path needs partial launch control.

## Verification

Run these after any follow-up code change:

```sh
cd virtie && go test ./...
cd virtie && go test -tags virtie_no_hotplug ./...
```
