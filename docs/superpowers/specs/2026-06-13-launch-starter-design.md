# Launch Starter Design

## Purpose

Deepen virtie's launch startup path so the transition from a planned launch to a
started runtime is owned by `launch.Starter` with small `Host` and `Runtime`
interfaces. The consumer CLI and manifest contracts stayed unchanged. Internal
Go entrypoints were allowed to move where that improved locality around startup
ordering, readiness, and cleanup.

## Implemented Topology

`manager.startWithPlan` now delegates planned-launch startup to
`launch.Starter`:

- `launch.Starter` owns runtime lock acquisition, launch PID cleanup, VSock CID
  acquisition, QEMU command finalization, runtime state preparation, process
  startup, readiness timing, restore, runtime construction, control startup,
  queued suspend draining, guest provisioning, SSH readiness, write-back
  enablement, and startup-failure cleanup.
- `manager.launchHost` owns concrete host effects behind `launch.Host`.
- `manager.launchRuntime` owns runtime construction, suspend handling, and
  foreground wait construction behind `launch.Runtime`.
- `launch.Stats`, `launch.TimerEvent`, and `launch.ProcessSet` live with
  startup because readiness timing and started process ownership are startup
  concerns.

The important invariant is the ordered composition of startup helpers. Starter
tests now exercise that composition directly instead of reaching through the
manager's concrete wiring.

## Goals Delivered

- Added `launch.Starter` as the deep module for planned-launch startup.
- Kept concrete host effects in the small `launch.Host` interface.
- Kept runtime-specific construction behind the small `launch.Runtime`
  interface.
- Moved launch timing stats and process ownership helpers into the `launch`
  package.
- Preserved user-visible CLI, manifest, stage error, readiness, restore,
  suspend, hotplug, balloon, and cleanup behavior.
- Kept hotplug removable by registering optional hotplug control handling from
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

The `launch` package owns startup orchestration:

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

`manager.startWithPlan` is wiring:

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
	// NewLifecycle creates the signal-aware lifecycle for the launch context.
	NewLifecycle(context.CancelFunc) *Lifecycle

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

	// InstallQMPShutdown configures QEMU shutdown to go through the serialized QMP client.
	InstallQMPShutdown(*executor.Process, qmpclient.Client)

	// WaitForSockets waits for startup sockets while watching already-started processes.
	WaitForSockets(context.Context, string, []string, executor.Group) error

	// WaitForQMP waits for QMP readiness and returns a connected client.
	WaitForQMP(context.Context, string, executor.Group) (qmpclient.Client, error)

	// RestoreRuntime restores a saved VM state through QMP.
	RestoreRuntime(context.Context, *Plan, qmpclient.Client) error

	// WriteGuestFiles provisions configured guest files and workspace mounts.
	WriteGuestFiles(context.Context, *Plan, *Stats, executor.Group) error

	// WriteBackGuestFiles writes configured guest files back to the host during close.
	WriteBackGuestFiles(context.Context, *Plan, executor.Group) error

	// WaitForSSHReady waits for the guest readiness signal used by SSH startup.
	WaitForSSHReady(context.Context, string, executor.Group) error

	// ShutdownDelay returns the process shutdown delay used for startup-failure cleanup.
	ShutdownDelay() time.Duration

	// StatsOutput returns the writer used for launch stats output.
	StatsOutput() io.Writer
}
```

The interface stays host-effect oriented. It does not include runtime control
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

`StartedRuntime` is satisfied by `runtime.Core`:

```go
type StartedRuntime interface {
	SetReady()
	MarkSavedSuspend()
	SetWatchers(executor.Group)
	StartControl(context.Context, ...control.RouterOption) (*control.Server, error)
	Wait(context.Context, WaitMode) error
	Close() error
	QMP() qmpclient.Client
}
```

`launch.Runtime` does not own stats creation, stats finalization, write-back
construction, or saved-suspend classification. `Starter` owns launch timing
directly in `launch`.

## Stats

Launch timing stats and process ownership helpers live in `manager/launch` so
`Starter` can own readiness timing and started process cleanup without importing
runtime internals.

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
same launch stats summary that the launch close hook prints.

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

`runtime.Core` exposes control status stats by consuming `*launch.Stats` through
`RuntimeConfig`. Structured control stats are mapped by `launch.ControlStats`,
beside `Stats`, so runtime does not need exported timer getters.

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
13. `Starter` returns the started runtime to `manager.startWithPlan`, which continues
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

## Testing Outcomes

Startup ordering tests moved toward `launch.Starter`:

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

Manager integration tests still cover concrete wiring:

- CLI/manifest behavior remains compatible.
- QEMU command construction remains compatible.
- Hotplug is registered at control periphery.
- Direct hotplug fallback remains compatible while it exists.
- Guest file provisioning and write-back continue to use QGA correctly.

## Migration Notes

- Internal tests that only assert startup ordering or cleanup now belong in
  `launch.Starter` tests.
- `Launcher.Plan` and `Launcher.Start` remain internal partial-lifecycle
  entrypoints for now and can be reconsidered separately.
- Stats and process ownership references use `launch.Stats` and
  `launch.ProcessSet`.
- The public CLI and manifest did not need a `MIGRATION.md` entry because the
  consumer contract is unchanged.
