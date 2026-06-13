# Launch Starter Design

## Purpose

Deepen virtie's launch startup path so the transition from a planned launch to a
started runtime is owned by one Module with a small Interface. The consumer CLI
and manifest contracts stay unchanged. Internal Go entrypoints may break where
that improves locality around startup ordering, readiness, and cleanup.

## Current Friction

`manager.startWithPlan` still owns most of launch startup:

- Runtime lock acquisition and launch PID cleanup.
- VSock CID acquisition and QEMU command finalization.
- Persistence directory creation, runtime socket cleanup, external socket
  validation, and volume image creation.
- Run command startup, virtiofs socket waits, QEMU startup, QMP readiness,
  QMP serialization, and QMP shutdown hook installation.
- Restore from saved suspend state.
- Runtime construction, control server startup, queued suspend draining, guest
  file provisioning, SSH readiness, and write-back enablement.
- Startup-failure cleanup across processes, QMP, sockets, lock state, and
  launch stats.

The Interface is shallow: tests and maintainers must understand the entire
startup Implementation to reason about small ordering changes. Helper functions
exist, but the important invariant is the ordered composition of those helpers,
not any single helper in isolation.

## Goals

- Add `launch.Starter` as the deep Module for planned-launch startup.
- Keep concrete host effects in a small `launch.Host` Interface.
- Keep runtime-specific construction behind a small `launch.Runtime` Interface.
- Move launch timing stats into the `launch` package.
- Preserve current user-visible CLI, manifest, stage error, readiness, restore,
  suspend, hotplug, balloon, and cleanup behavior.
- Keep hotplug removable by registering optional hotplug control handling from
  manager-owned runtime construction, not from runtime core startup internals.

## Non-Goals

- Do not change the manifest schema.
- Do not change the `virtie launch`, `virtie suspend`, `virtie hotplug`,
  status, info, or balloon command behavior.
- Do not move foreground SSH session behavior into `runtime.Core`.
- Do not broaden hotplug ownership beyond manager/control periphery.
- Do not keep `Launcher.Plan` or `Launcher.Start` stable for internal callers if
  a cleaner startup Interface makes them unnecessary.

## Module Shape

The `launch` package owns the startup orchestration:

```go
type Starter struct {
	Host    Host
	Runtime Runtime
}

func (s Starter) Start(ctx context.Context, plan *Plan) (StartedRuntime, error)
```

`Starter` owns:

- Launch context and signal lifecycle creation.
- Runtime lock acquisition and cleanup.
- CID/QEMU finalization.
- Runtime state preparation.
- Process startup through QMP readiness.
- Restore timing and write-back enablement.
- Runtime construction.
- Control startup with optional runtime-provided handlers.
- Queued suspend handling.
- Guest provisioning and SSH readiness.
- Startup failure cleanup.
- Launch stats timing and finalization.

`manager.startWithPlan` becomes wiring:

```go
starter := launch.Starter{
	Host:    launchHost{manager: m},
	Runtime: launchRuntime{manager: m},
}

return starter.Start(ctx, plan)
```

## Host Interface

`launch.Host` provides concrete host-side effects that are fakeable in starter
tests and implemented by `manager.launchHost`:

```go
type Host interface {
	// AcquireRuntimeLock obtains the sandbox runtime lock and launch PID.
	AcquireRuntimeLock(RuntimeLockSpec) (*RuntimeLock, error)

	// AcquireCID chooses the VSock CID for fresh launch or restore.
	AcquireCID(*manifest.Manifest, *SuspendState) (int, error)

	// BuildQEMUCommand finalizes the QEMU command for the selected CID.
	BuildQEMUCommand(*manifest.Manifest, int, bool) (*exec.Cmd, error)

	// PrepareRuntimeState creates directories, validates external sockets, removes stale sockets, and creates volumes.
	PrepareRuntimeState(*Plan) error

	// RemoveSocketPaths removes runtime socket cleanup files.
	RemoveSocketPaths([]string) error

	// StartRuns starts configured host-side run commands.
	StartRuns(int, *manifest.Manifest) (executor.Group, error)

	// StartQEMU starts the finalized QEMU command.
	StartQEMU(*exec.Cmd) (*executor.Process, error)

	// WaitForSockets waits for startup sockets while watching already-started processes.
	WaitForSockets(context.Context, string, []string, executor.Group) error

	// WaitForQMP waits for QMP readiness and returns a connected client.
	WaitForQMP(context.Context, string, executor.Group) (qmpclient.Client, error)

	// RestoreRuntime restores a saved VM state through QMP.
	RestoreRuntime(context.Context, *Plan, qmpclient.Client) error

	// WriteGuestFiles provisions configured guest files and workspace mounts.
	WriteGuestFiles(context.Context, *Plan, *Stats, executor.Group) error

	// WaitForSSHReady waits for the guest readiness signal used by SSH startup.
	WaitForSSHReady(context.Context, string, executor.Group) error
}
```

The Interface stays host-effect oriented. It should not include runtime control
policy, hotplug policy, foreground wait policy, or stats formatting.

## Runtime Interface

`launch.Runtime` wires a fully-started VM into the long-lived runtime object:

```go
type Runtime interface {
	// New builds the runtime and optional control handlers from started VM state.
	New(RuntimeSpec) (RuntimeResult, error)

	// SuspendHandler builds the handler used for queued and signal-driven suspend requests.
	SuspendHandler(SuspendSpec) SuspendHandler

	// WaitForeground builds the foreground wait function stored on the runtime.
	WaitForeground(ForegroundSpec) func(context.Context, *Plan) error
}
```

`RuntimeResult` keeps optional control handling near runtime construction
without making `Starter` know about hotplug:

```go
type RuntimeResult struct {
	Runtime        StartedRuntime
	ControlOptions []control.RouterOption
}
```

`StartedRuntime` is package-local to launch and satisfied by `runtime.Core`:

```go
type StartedRuntime interface {
	SetReady()
	MarkSavedSuspend()
	StartControl(context.Context, ...control.RouterOption) (*control.Server, error)
	Wait(context.Context, WaitMode) error
	Close() error
	QMP() qmpclient.Client
}
```

`launch.Runtime` does not own stats creation, stats finalization, write-back
construction, or saved-suspend classification. `Starter` owns launch timing
directly after stats move into `launch`.

## Stats

Move launch timing stats from `manager/runtime` into `manager/launch`.

Stats use one timer Interface rather than many phase-specific methods. The
stored timings are map-backed so adding a new startup timer does not require a
new struct field or exported getter:

```go
type TimerEvent string

const (
	TimerStarted         TimerEvent = "started"
	TimerBootStarted     TimerEvent = "boot_started"
	TimerQMPReady        TimerEvent = "qmp_ready"
	TimerGuestAgentReady TimerEvent = "guest_agent_ready"
	TimerFilesReady      TimerEvent = "files_ready"
	TimerSSHReady        TimerEvent = "ssh_ready"
	TimerSSHAttempt      TimerEvent = "ssh_attempt"
	TimerSSHStarted      TimerEvent = "ssh_started"
	TimerCompleted       TimerEvent = "completed"
)

type Stats struct {
	timers map[TimerEvent]time.Time
	counts map[TimerEvent]int
}

func NewStats(started time.Time) *Stats
func (s *Stats) Timer(event TimerEvent, t time.Time)
func (s *Stats) String() string
```

`NewStats` records `TimerStarted` immediately. For ordinary events, `Timer`
sets `timers[event] = t`. For `TimerSSHAttempt`, `Timer` increments the count
and records only the first attempt time in the timers map. `String` formats the
same launch stats summary currently printed by `runtimepkg.Stats.String`.

Stats finalization is not a `Stats` method. The runtime close hook records
`TimerCompleted` and writes the summary when output is configured:

```go
func finalizeStats(stats *Stats, output io.Writer) func() {
	return func() {
		if stats == nil {
			return
		}
		stats.Timer(TimerCompleted, time.Now())
		if output != nil {
			fmt.Fprintf(output, "stats: %s\n", stats)
		}
	}
}
```

`runtime.Core` can continue to expose control status stats by consuming
`*launch.Stats` through `RuntimeConfig`. The formatting and control response
mapping can move with the stats type, or remain as thin runtime helpers during
the first migration. Runtime helpers should not require exported timer getters;
if structured control stats need package-private timer access, put that mapping
beside `Stats` in `launch`.

## Data Flow

Startup flow after the refactor:

1. `manager` builds a `launch.Plan` as today.
2. `manager.startWithPlan` constructs `launch.Starter` with `launchHost` and
   `launchRuntime`.
3. `Starter.Start` creates stats, launch context, and lifecycle.
4. `Starter` acquires the runtime lock.
5. `Starter` acquires CID, builds QEMU command, mutates the plan with `CID` and
   `QEMUCommand`, and prepares runtime state.
6. `Starter` creates a process set and starts run commands, waits virtiofs
   sockets, starts QEMU, waits QMP, serializes QMP, and installs QMP shutdown.
7. `Starter` restores saved VM state when present and enables write-back.
8. `Starter` builds the suspend handler and foreground wait function through
   `launch.Runtime`.
9. `Starter` builds the long-lived runtime through `launch.Runtime.New`.
10. `Starter` marks runtime ready and starts control with returned options.
11. `Starter` drains queued suspend requests.
12. For fresh launches, `Starter` writes guest files, waits SSH readiness when
    configured, records readiness timers, and enables write-back.
13. `Starter` returns the started runtime to `launchWithPlan`, which continues
    to call `runtime.Wait` and defer `runtime.Close`.

## Error And Cleanup Behavior

Stage names remain compatible:

- `preflight`
- `run startup`
- `virtiofs startup`
- `vm startup`
- `restore`
- `control startup`
- `guest agent`
- existing guest file and SSH readiness stage wrappers

Startup-failure cleanup remains ordered and idempotent:

1. If runtime construction succeeded, close the runtime and let runtime close
   own write-back, control shutdown, process shutdown, QMP disconnect, socket
   cleanup, lock cleanup, and stats finalization.
2. If runtime construction did not succeed, stop started processes, release the
   runtime lock, disconnect QMP if connected, remove runtime socket cleanup
   files, and finalize stats.
3. If the failure is the saved-suspend exit sentinel after runtime construction,
   mark the runtime as saved suspend before closing so write-back is skipped.

## Testing Strategy

Move startup ordering tests toward `launch.Starter`:

- Runtime lock cleanup happens on preflight and startup failure.
- CID/QEMU command finalization occurs before runtime state preparation.
- Runtime state preparation removes stale socket paths and validates external
  virtiofs sockets before processes start.
- Run commands start before virtiofs socket waits and QEMU.
- QMP is serialized before runtime construction.
- Restore runs before runtime construction.
- Control starts after runtime readiness and uses runtime-provided optional
  control handlers.
- Queued suspend drains after control startup and before fresh guest
  provisioning.
- Fresh launch guest files and SSH readiness run after control startup.
- Startup failure cleanup stops processes, disconnects QMP, removes sockets,
  releases runtime lock, and finalizes stats.

Keep manager integration tests for concrete wiring:

- CLI/manifest behavior remains compatible.
- QEMU command construction remains compatible.
- Hotplug is registered at control periphery.
- Direct hotplug fallback remains compatible while it exists.
- Guest file provisioning and write-back continue to use QGA correctly.

## Migration Notes

- Internal tests that call `manager.startWithPlan` can move to `launch.Starter`
  tests where they only assert startup ordering or cleanup.
- `Launcher.Plan` and `Launcher.Start` may become private or be removed if no
  production caller needs partial lifecycle control.
- `runtimepkg.Stats` references migrate to `launch.Stats`. Compatibility helper
  functions can remain temporarily in `runtime` only if they reduce review risk.
- The public CLI and manifest do not need a `MIGRATION.md` entry because the
  consumer contract is unchanged.
