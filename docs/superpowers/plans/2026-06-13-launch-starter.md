# Launch Starter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move planned-launch startup ordering, readiness, launch stats, and startup-failure cleanup behind `launch.Starter` while preserving the virtie CLI and manifest behavior.

**Architecture:** `launch.Starter` becomes the deep Module that turns a `launch.Plan` into a started runtime. `launch.Host` supplies concrete host-side effects, and `launch.Runtime` supplies runtime construction, suspend handling, and foreground wait wiring. Launch timing stats and process ownership helpers move into `launch` so `Starter` can own startup without importing `manager/runtime`.

**Tech Stack:** Go, existing `virtie/internal/manager/{launch,runtime,control}` packages, standard `testing`, existing fake manager test helpers.

---

## File Structure

- Modify: `virtie/internal/manager/launch/stats.go`
  - New home for launch timing stats, timer events, `String`, control stats mapping, and package-private stats finalization.
- Modify: `virtie/internal/manager/launch/stats_test.go`
  - New tests moved from runtime stats tests and updated for `TimerEvent`.
- Modify: `virtie/internal/manager/runtime/stats.go`
  - Remove after migration.
- Modify: `virtie/internal/manager/runtime/stats_test.go`
  - Remove after migration.
- Modify: `virtie/internal/manager/launch/process_set.go`
  - New home for `ProcessSet`.
- Modify: `virtie/internal/manager/launch/task.go`
  - New home for runtime task helpers used by `ProcessSet`.
- Modify: `virtie/internal/manager/launch/process_set_test.go`
  - New tests moved from runtime process set tests.
- Modify: `virtie/internal/manager/launch/task_test.go`
  - New tests moved from runtime task tests.
- Modify: `virtie/internal/manager/runtime/process_set.go`
  - Remove after migration.
- Modify: `virtie/internal/manager/runtime/task.go`
  - Remove after migration.
- Modify: `virtie/internal/manager/runtime/process_set_test.go`
  - Remove after migration.
- Modify: `virtie/internal/manager/runtime/task_test.go`
  - Remove after migration.
- Create: `virtie/internal/manager/launch/starter.go`
  - Defines `Starter`, `Host`, `Runtime`, `RuntimeSpec`, `RuntimeResult`, `StartedRuntime`, `SuspendHandler`, saved-suspend sentinel helpers, and startup data structs.
- Create: `virtie/internal/manager/launch/starter_test.go`
  - Unit tests for ordering, cleanup, restore, control registration, queued suspend, guest provisioning, SSH readiness, and stats finalization.
- Modify: `virtie/internal/manager/runtime/dependencies.go`
  - Change `Stats` and `Processes` fields to launch-owned types.
- Modify: `virtie/internal/manager/runtime/concrete.go`
  - Use `*launch.Stats` and `*launch.ProcessSet`; keep runtime focused on already-started lifecycle.
- Modify: `virtie/internal/manager/runtime/lifecycle.go`
  - Replace runtime-local `controlStats` with launch stats mapping.
- Modify: `virtie/internal/manager/runtime/lifecycle_test.go`
  - Update stats construction and timer calls.
- Modify: `virtie/internal/manager/runtime/closer_test.go`
  - Update process set package references.
- Modify: `virtie/internal/manager/runtime/lifecycle_test.go`
  - Update process set package references where needed.
- Modify: `virtie/internal/manager/runtime_test.go`
  - Update stats/process imports and expected behavior.
- Create: `virtie/internal/manager/launch_host.go`
  - Implements `launch.Host` using manager dependencies.
- Create: `virtie/internal/manager/launch_runtime.go`
  - Implements `launch.Runtime` using `runtimepkg.Core`, suspend handler, foreground wait, hotplug control registration, write-back close hooks, and stats finalization.
- Modify: `virtie/internal/manager/manager.go`
  - Shrink `startWithPlan`; remove startup orchestration that moves into `launch.Starter`; keep concrete helper methods that `launchHost`/`launchRuntime` use.
- Modify: `virtie/internal/manager/guest_agent.go`
  - Change stats argument from `*runtimepkg.Stats` to `*launch.Stats` and use `Timer`.
- Modify: `virtie/internal/manager/launcher.go`
  - Decide whether `Launcher.Plan`/`Launcher.Start` stay as wrappers or are removed. If retained, `Start` returns `launch.StartedRuntime` or remains private.
- Modify: `virtie/internal/manager/launch/api_surface_test.go`
  - Allow intended exports: `Starter`, `Host`, `Runtime`, `RuntimeResult`, `RuntimeSpec`, `StartedRuntime`, `SuspendHandler`, `Stats`, `TimerEvent`, and timer constants.
- Modify: `virtie/internal/manager/runtime/api_surface_test.go`
  - Remove runtime stats/process exports from allowed production surface if they disappear.
- Modify: `.specs/virtie-manager-refactor.md`
  - Update launch flow/package map to mention `launch.Starter`, launch-owned stats, and launch-owned process set.
- Modify: `docs/superpowers/specs/2026-06-13-launch-starter-design.md`
  - Keep design aligned with any field-level changes discovered during implementation.

## Task 1: Move Launch Stats Into `launch`

**Files:**
- Create: `virtie/internal/manager/launch/stats.go`
- Create: `virtie/internal/manager/launch/stats_test.go`
- Modify: `virtie/internal/manager/runtime/stats.go`
- Modify: `virtie/internal/manager/runtime/stats_test.go`
- Modify: `virtie/internal/manager/runtime/dependencies.go`
- Modify: `virtie/internal/manager/runtime/concrete.go`
- Modify: `virtie/internal/manager/runtime/lifecycle.go`
- Modify: `virtie/internal/manager/runtime/lifecycle_test.go`
- Modify: `virtie/internal/manager/runtime_test.go`
- Modify: `virtie/internal/manager/guest_agent.go`
- Modify: `virtie/internal/manager/manager.go`

- [ ] **Step 1: Write the failing launch stats tests**

Create `virtie/internal/manager/launch/stats_test.go` with:

```go
package launch

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStatsStringFromTimerEvents(t *testing.T) {
	started := time.Unix(100, 0)
	stats := NewStats(started)
	stats.Timer(TimerBootStarted, started.Add(10*time.Millisecond))
	stats.Timer(TimerQMPReady, started.Add(30*time.Millisecond))
	stats.Timer(TimerGuestAgentReady, started.Add(40*time.Millisecond))
	stats.Timer(TimerFilesReady, started.Add(60*time.Millisecond))
	stats.Timer(TimerSSHAttempt, started.Add(80*time.Millisecond))
	stats.Timer(TimerSSHStarted, started.Add(100*time.Millisecond))
	stats.Timer(TimerCompleted, started.Add(150*time.Millisecond))

	got := stats.String()
	for _, want := range []string{
		"started_to_boot=10ms",
		"boot_to_qmp=20ms",
		"qmp_to_guest_agent=10ms",
		"guest_agent_to_files=20ms",
		"files_to_first_ssh=20ms",
		"files_to_ssh=40ms",
		"ssh_to_completed=50ms",
		"total=150ms",
		"ssh_attempts=1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stats string %q missing %q", got, want)
		}
	}
}

func TestStatsTimerSSHAttemptKeepsFirstAttemptAndCountsAllAttempts(t *testing.T) {
	started := time.Unix(100, 0)
	stats := NewStats(started)
	stats.Timer(TimerFilesReady, started.Add(10*time.Millisecond))
	stats.Timer(TimerSSHAttempt, started.Add(20*time.Millisecond))
	stats.Timer(TimerSSHAttempt, started.Add(30*time.Millisecond))
	stats.Timer(TimerSSHStarted, started.Add(40*time.Millisecond))

	got := stats.String()
	for _, want := range []string{"files_to_first_ssh=10ms", "files_to_ssh=30ms", "ssh_attempts=2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stats string %q missing %q", got, want)
		}
	}
}

func TestControlStatsFromTimerEvents(t *testing.T) {
	started := time.Unix(100, 0)
	stats := NewStats(started)
	stats.Timer(TimerBootStarted, started.Add(time.Second))
	stats.Timer(TimerQMPReady, started.Add(2*time.Second))
	stats.Timer(TimerFilesReady, started.Add(3*time.Second))
	stats.Timer(TimerSSHReady, started.Add(5*time.Second))
	stats.Timer(TimerCompleted, started.Add(8*time.Second))
	stats.Timer(TimerSSHAttempt, started.Add(4*time.Second))

	got := ControlStats(stats)
	if got.StartedAt != started || got.BootStartedAt != started.Add(time.Second) {
		t.Fatalf("unexpected timestamps: %#v", got)
	}
	if got.StartedToBoot != "1s" || got.BootToQMP != "1s" || got.FilesToSSH != "2s" || got.BootToCompleted != "7s" || got.Total != "8s" {
		t.Fatalf("unexpected durations: %#v", got)
	}
	if got.SSHAttempts != 1 {
		t.Fatalf("unexpected ssh attempts: got %d want 1", got.SSHAttempts)
	}
}

func TestFinalizeStatsMarksCompletedAndWritesOutput(t *testing.T) {
	started := time.Now().Add(-time.Second)
	stats := NewStats(started)
	stats.Timer(TimerBootStarted, time.Now().Add(-500*time.Millisecond))
	var output bytes.Buffer

	finalizeStats(stats, &output)()

	got := output.String()
	if !strings.HasPrefix(got, "stats: ") || !strings.Contains(got, "total=") {
		t.Fatalf("unexpected stats output: %q", got)
	}
	if ControlStats(stats).CompletedAt.IsZero() {
		t.Fatal("stats finalizer did not mark completion")
	}
}
```

- [ ] **Step 2: Run the new stats tests and verify they fail**

Run:

```bash
cd virtie && go test ./internal/manager/launch -run 'TestStats|TestControlStats|TestFinalizeStats' -count=1
```

Expected: FAIL because `NewStats`, `TimerEvent`, `ControlStats`, and `finalizeStats` are not yet defined in `launch`.

- [ ] **Step 3: Implement launch stats**

Create `virtie/internal/manager/launch/stats.go`:

```go
package launch

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

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

func NewStats(started time.Time) *Stats {
	stats := &Stats{
		timers: map[TimerEvent]time.Time{},
		counts: map[TimerEvent]int{},
	}
	stats.Timer(TimerStarted, started)
	return stats
}

func (s *Stats) Timer(event TimerEvent, t time.Time) {
	if s == nil {
		return
	}
	if s.timers == nil {
		s.timers = map[TimerEvent]time.Time{}
	}
	if s.counts == nil {
		s.counts = map[TimerEvent]int{}
	}
	if event == TimerSSHAttempt {
		s.counts[event]++
		if s.timers[event].IsZero() {
			s.timers[event] = t
		}
		return
	}
	s.timers[event] = t
}

func (s *Stats) String() string {
	if s == nil {
		return ""
	}
	var fields []string
	started := s.time(TimerStarted)
	bootStarted := s.time(TimerBootStarted)
	qmpReady := s.time(TimerQMPReady)
	guestAgentReady := s.time(TimerGuestAgentReady)
	filesReady := s.time(TimerFilesReady)
	firstSSHAttempt := s.time(TimerSSHAttempt)
	sshStarted := s.time(TimerSSHStarted)
	completed := s.time(TimerCompleted)
	sshReady := s.sshReady()

	if !started.IsZero() && !bootStarted.IsZero() {
		fields = append(fields, formatStatDuration("started_to_boot", bootStarted.Sub(started)))
	}
	if !bootStarted.IsZero() && !qmpReady.IsZero() {
		fields = append(fields, formatStatDuration("boot_to_qmp", qmpReady.Sub(bootStarted)))
	}
	if !qmpReady.IsZero() && !guestAgentReady.IsZero() {
		fields = append(fields, formatStatDuration("qmp_to_guest_agent", guestAgentReady.Sub(qmpReady)))
	}
	if !guestAgentReady.IsZero() && !filesReady.IsZero() {
		fields = append(fields, formatStatDuration("guest_agent_to_files", filesReady.Sub(guestAgentReady)))
	}
	if !filesReady.IsZero() && !firstSSHAttempt.IsZero() {
		fields = append(fields, formatStatDuration("files_to_first_ssh", firstSSHAttempt.Sub(filesReady)))
	}
	if !filesReady.IsZero() && !sshReady.IsZero() {
		fields = append(fields, formatStatDuration("files_to_ssh", sshReady.Sub(filesReady)))
	}
	if !bootStarted.IsZero() && !sshReady.IsZero() {
		fields = append(fields, formatStatDuration("boot_to_ssh", sshReady.Sub(bootStarted)))
	}
	if !sshStarted.IsZero() && !completed.IsZero() {
		fields = append(fields, formatStatDuration("ssh_to_completed", completed.Sub(sshStarted)))
	}
	if sshStarted.IsZero() && !bootStarted.IsZero() && !completed.IsZero() {
		fields = append(fields, formatStatDuration("boot_to_completed", completed.Sub(bootStarted)))
	}
	if !started.IsZero() && !completed.IsZero() {
		fields = append(fields, formatStatDuration("total", completed.Sub(started)))
	}
	if attempts := s.count(TimerSSHAttempt); attempts > 0 {
		fields = append(fields, fmt.Sprintf("ssh_attempts=%d", attempts))
	}
	return strings.Join(fields, " ")
}

func ControlStats(stats *Stats) control.RuntimeStats {
	if stats == nil {
		return control.RuntimeStats{}
	}
	started := stats.time(TimerStarted)
	bootStarted := stats.time(TimerBootStarted)
	qmpReady := stats.time(TimerQMPReady)
	filesReady := stats.time(TimerFilesReady)
	sshReady := stats.sshReady()
	completed := stats.time(TimerCompleted)
	resp := control.RuntimeStats{
		StartedAt:     started,
		BootStartedAt: bootStarted,
		QMPReadyAt:    qmpReady,
		FilesReadyAt:  filesReady,
		SSHReadyAt:    stats.time(TimerSSHReady),
		SSHStartedAt:  stats.time(TimerSSHStarted),
		CompletedAt:   completed,
		SSHAttempts:   stats.count(TimerSSHAttempt),
	}
	if !started.IsZero() && !bootStarted.IsZero() {
		resp.StartedToBoot = bootStarted.Sub(started).String()
	}
	if !bootStarted.IsZero() && !qmpReady.IsZero() {
		resp.BootToQMP = qmpReady.Sub(bootStarted).String()
	}
	if !filesReady.IsZero() && !sshReady.IsZero() {
		resp.FilesToSSH = sshReady.Sub(filesReady).String()
	}
	if !bootStarted.IsZero() && !completed.IsZero() {
		resp.BootToCompleted = completed.Sub(bootStarted).String()
	}
	if !started.IsZero() && !completed.IsZero() {
		resp.Total = completed.Sub(started).String()
	}
	return resp
}

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

func (s *Stats) time(event TimerEvent) time.Time {
	if s == nil || s.timers == nil {
		return time.Time{}
	}
	return s.timers[event]
}

func (s *Stats) count(event TimerEvent) int {
	if s == nil || s.counts == nil {
		return 0
	}
	return s.counts[event]
}

func (s *Stats) sshReady() time.Time {
	if ready := s.time(TimerSSHReady); !ready.IsZero() {
		return ready
	}
	return s.time(TimerSSHStarted)
}

func formatStatDuration(name string, duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	return fmt.Sprintf("%s=%s", name, duration)
}
```

- [ ] **Step 4: Update runtime to consume launch stats**

Edit `virtie/internal/manager/runtime/dependencies.go`:

```go
type RuntimeConfig struct {
	Manifest        *manifest.Manifest
	Plan            *launch.Plan
	Paths           launch.RuntimePaths
	CID             int
	Stats           *launch.Stats
	QMP             qmpclient.Client
	SuspendRequests *launch.SuspendCoordinator
	Processes       *ProcessSet
	ShutdownDelay   time.Duration
	WaitForeground  func(context.Context, *launch.Plan) error
	CloseHooks      CloseHooks
	Dependencies    Dependencies
}
```

Edit `virtie/internal/manager/runtime/concrete.go` so `Core.stats` is `*launch.Stats`.

Edit `virtie/internal/manager/runtime/lifecycle.go`:

```go
func status(state *state, cid int, paths control.StatusPaths, stats *launch.Stats) control.StatusResponse {
	return control.StatusResponse{
		State: state.Current(),
		CID:   cid,
		Paths: paths,
		Stats: launch.ControlStats(stats),
	}
}
```

- [ ] **Step 5: Update manager stats call sites**

Replace:

```go
stats := runtimepkg.NewStats(time.Now())
```

with:

```go
stats := launch.NewStats(time.Now())
```

Replace timer calls in `manager.go`:

```go
stats.MarkBootStarted(time.Now())
stats.MarkQMPReady(time.Now())
stats.MarkFilesReady(time.Now())
stats.MarkSSHReady(time.Now())
runtimepkg.StatsFinalizer(stats, m.outputWriter())
```

with:

```go
stats.Timer(launch.TimerBootStarted, time.Now())
stats.Timer(launch.TimerQMPReady, time.Now())
stats.Timer(launch.TimerFilesReady, time.Now())
stats.Timer(launch.TimerSSHReady, time.Now())
launchStatsCloseHook(stats, m.outputWriter())
```

Add a temporary manager helper in `manager.go` near `joinDeferredError`; Task 6 removes this once `launch.Starter` owns finalization:

```go
func launchStatsCloseHook(stats *launch.Stats, output io.Writer) func() {
	return func() {
		if stats == nil {
			return
		}
		stats.Timer(launch.TimerCompleted, time.Now())
		if output != nil {
			fmt.Fprintf(output, "stats: %s\n", stats)
		}
	}
}
```

Add `io` to `manager.go` imports if needed.

Edit `virtie/internal/manager/guest_agent.go`:

```go
func (m *manager) writeGuestFiles(ctx context.Context, launchManifest *manifest.Manifest, stats *launch.Stats, watchers executor.Group) error {
	// ...
	if stats != nil {
		stats.Timer(launch.TimerGuestAgentReady, time.Now())
	}
	// ...
}
```

- [ ] **Step 6: Update SSH session stats call sites**

In `manager.runSSHSession`, replace direct method values:

```go
MarkSSHAttempt: stats.MarkSSHAttempt,
MarkSSHStarted: stats.MarkSSHStarted,
```

with wrappers:

```go
MarkSSHAttempt: func(t time.Time) { stats.Timer(launch.TimerSSHAttempt, t) },
MarkSSHStarted: func(t time.Time) { stats.Timer(launch.TimerSSHStarted, t) },
```

- [ ] **Step 7: Delete runtime stats implementation and update tests**

Remove `virtie/internal/manager/runtime/stats.go` and `virtie/internal/manager/runtime/stats_test.go`.

Update tests that construct stats:

```go
stats := launch.NewStats(time.Now())
stats.Timer(launch.TimerBootStarted, time.Now().Add(time.Second))
```

Update imports in `virtie/internal/manager/runtime_test.go` and runtime package tests to include `github.com/shazow/agentspace/virtie/internal/manager/launch`.

- [ ] **Step 8: Run stats migration tests**

Run:

```bash
cd virtie && go test ./internal/manager/launch ./internal/manager/runtime ./internal/manager -run 'TestStats|TestControlStats|TestFinalizeStats|TestMarkReadyAndStatus|TestRuntime|TestManagerLaunch' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit stats migration**

```bash
git add virtie/internal/manager/launch/stats.go virtie/internal/manager/launch/stats_test.go virtie/internal/manager/runtime/stats.go virtie/internal/manager/runtime/stats_test.go virtie/internal/manager/runtime/dependencies.go virtie/internal/manager/runtime/concrete.go virtie/internal/manager/runtime/lifecycle.go virtie/internal/manager/runtime/lifecycle_test.go virtie/internal/manager/runtime_test.go virtie/internal/manager/guest_agent.go virtie/internal/manager/manager.go
git commit -m "virtie: Move launch stats into launch package" \
  -m "Launch timing now uses map-backed TimerEvent stats in manager/launch. Runtime consumes launch stats for status reporting while manager call sites record timer events through the launch-owned API." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager/launch ./internal/manager/runtime ./internal/manager -run 'TestStats|TestControlStats|TestFinalizeStats|TestMarkReadyAndStatus|TestRuntime|TestManagerLaunch' -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

## Task 2: Move Process Ownership Helpers Into `launch`

**Files:**
- Create: `virtie/internal/manager/launch/process_set.go`
- Create: `virtie/internal/manager/launch/process_set_test.go`
- Create: `virtie/internal/manager/launch/task.go`
- Create: `virtie/internal/manager/launch/task_test.go`
- Delete: `virtie/internal/manager/runtime/process_set.go`
- Delete: `virtie/internal/manager/runtime/process_set_test.go`
- Delete: `virtie/internal/manager/runtime/task.go`
- Delete: `virtie/internal/manager/runtime/task_test.go`
- Modify: `virtie/internal/manager/runtime/dependencies.go`
- Modify: `virtie/internal/manager/runtime/concrete.go`
- Modify: `virtie/internal/manager/runtime/closer.go`
- Modify: `virtie/internal/manager/runtime/closer_test.go`
- Modify: `virtie/internal/manager/runtime_test.go`
- Modify: `virtie/internal/manager/manager.go`
- Modify: `virtie/internal/manager/manager_test.go`

- [ ] **Step 1: Move process tests first**

Move the contents of `virtie/internal/manager/runtime/process_set_test.go` to `virtie/internal/manager/launch/process_set_test.go` and change the package declaration to:

```go
package launch
```

Move the contents of `virtie/internal/manager/runtime/task_test.go` to `virtie/internal/manager/launch/task_test.go` and change the package declaration to:

```go
package launch
```

- [ ] **Step 2: Run moved process tests and verify they fail**

Run:

```bash
cd virtie && go test ./internal/manager/launch -run 'TestProcessSet|TestTask|TestStartTask' -count=1
```

Expected: FAIL because `ProcessSet`, `NewProcessSet`, and task helpers are not yet in `launch`.

- [ ] **Step 3: Move process implementation**

Move `runtime/process_set.go` to `launch/process_set.go` and change the package declaration:

```go
package launch
```

Move `runtime/task.go` to `launch/task.go` and change the package declaration:

```go
package launch
```

Keep exported names unchanged:

```go
type ProcessSet struct { /* existing fields */ }
func NewProcessSet() *ProcessSet
func (p *ProcessSet) Add(processes ...*executor.Process)
func (p *ProcessSet) AddGroup(group executor.Group)
func (p *ProcessSet) SetQEMU(process *executor.Process)
func (p *ProcessSet) QEMU() *executor.Process
func (p *ProcessSet) Remove(process *executor.Process) bool
func (p *ProcessSet) Watchers() executor.Group
func (p *ProcessSet) VMWatchers() executor.Group
func (p *ProcessSet) StartTasks(ctx context.Context, tasks ...func(context.Context) error)
func (p *ProcessSet) Close(delay time.Duration) error
```

- [ ] **Step 4: Update runtime config to use launch process set**

In `virtie/internal/manager/runtime/dependencies.go`, change:

```go
Processes *ProcessSet
```

to:

```go
Processes *launch.ProcessSet
```

In `runtime/concrete.go`, change `Core.processes` to:

```go
processes *launch.ProcessSet
```

In `runtime/closer.go`, add the launch import and change:

```go
Processes *ProcessSet
```

to:

```go
Processes *launch.ProcessSet
```

- [ ] **Step 5: Update manager process call sites**

In `manager.go`, replace:

```go
processes := runtimepkg.NewProcessSet()
```

with:

```go
processes := launch.NewProcessSet()
```

Change helper signatures:

```go
func (m *manager) startLaunchRuntime(ctx context.Context, plan *launch.Plan, stats *launch.Stats, processes *launch.ProcessSet) (qmpclient.Client, error)
func (m *manager) waitForLaunchForeground(ctx context.Context, plan *launch.Plan, stats *launch.Stats, runtime launch.StartedRuntime, qmpClient qmpclient.Client, lifecycle *launch.Lifecycle, suspendHandler launch.SuspendHandler, processes *launch.ProcessSet) error
func (m *manager) runSSHSession(ctx context.Context, plan *launch.Plan, stats *launch.Stats, lifecycle *launch.Lifecycle, suspendHandler *launchSuspendHandler, processes *launch.ProcessSet) error
```

- [ ] **Step 6: Remove runtime process files**

Delete:

```bash
rm virtie/internal/manager/runtime/process_set.go virtie/internal/manager/runtime/process_set_test.go virtie/internal/manager/runtime/task.go virtie/internal/manager/runtime/task_test.go
```

- [ ] **Step 7: Run process migration tests**

Run:

```bash
cd virtie && go test ./internal/manager/launch ./internal/manager/runtime ./internal/manager -run 'TestProcessSet|TestTask|TestRuntime|TestManagerLaunch' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit process migration**

```bash
git add virtie/internal/manager/launch/process_set.go virtie/internal/manager/launch/process_set_test.go virtie/internal/manager/launch/task.go virtie/internal/manager/launch/task_test.go virtie/internal/manager/runtime/process_set.go virtie/internal/manager/runtime/process_set_test.go virtie/internal/manager/runtime/task.go virtie/internal/manager/runtime/task_test.go virtie/internal/manager/runtime/dependencies.go virtie/internal/manager/runtime/concrete.go virtie/internal/manager/runtime/closer.go virtie/internal/manager/runtime/closer_test.go virtie/internal/manager/runtime_test.go virtie/internal/manager/manager.go virtie/internal/manager/manager_test.go
git commit -m "virtie: Move launch process ownership into launch" \
  -m "ProcessSet and task helpers now live with launch startup so launch.Starter can own started process ordering and cleanup without importing runtime internals." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager/launch ./internal/manager/runtime ./internal/manager -run 'TestProcessSet|TestTask|TestRuntime|TestManagerLaunch' -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

## Task 3: Add `launch.Starter` Surface And Core Tests

**Files:**
- Create: `virtie/internal/manager/launch/starter.go`
- Create: `virtie/internal/manager/launch/starter_test.go`
- Modify: `virtie/internal/manager/launch/api_surface_test.go`

- [ ] **Step 1: Write starter API surface test**

In `virtie/internal/manager/launch/api_surface_test.go`, keep the existing banned helper list and add a positive test:

```go
func TestLaunchPackageExportsStarterSurface(t *testing.T) {
	names := exportedDecls(t)
	for _, name := range []string{
		"Starter",
		"Host",
		"Runtime",
		"RuntimeSpec",
		"RuntimeResult",
		"StartedRuntime",
		"SuspendHandler",
		"SuspendSpec",
		"ForegroundSpec",
		"TimerEvent",
		"Stats",
	} {
		if _, ok := names[name]; !ok {
			t.Fatalf("launch should export %s", name)
		}
	}
}
```

- [ ] **Step 2: Write starter ordering test**

Create `virtie/internal/manager/launch/starter_test.go` with this first test and fakes:

```go
package launch

import (
	"context"
	"io"
	"os/exec"
	"reflect"
	"testing"
	"time"

	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

func TestStarterFreshLaunchOrdersStartupAndReadiness(t *testing.T) {
	plan := testStarterPlan(t)
	host := &fakeStarterHost{}
	runtimeProvider := &fakeStarterRuntime{events: &host.events}
	starter := Starter{Host: host, Runtime: runtimeProvider}

	started, err := starter.Start(context.Background(), plan)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if started != runtimeProvider.runtime {
		t.Fatalf("started runtime mismatch")
	}
	wantEvents := []string{
		"lock",
		"cid",
		"qemu-command",
		"prepare",
		"start-runs",
		"wait-sockets:virtiofs startup",
		"start-qemu",
		"wait-qmp",
		"install-qmp-shutdown",
		"runtime-suspend-handler",
		"runtime-wait-foreground",
		"runtime-new",
		"runtime-ready",
		"runtime-control",
		"write-guest-files",
		"wait-ssh-ready",
	}
	if !reflect.DeepEqual(host.events, wantEvents) {
		t.Fatalf("events: got %#v want %#v", host.events, wantEvents)
	}
	if plan.CID != 7 || plan.QEMUCommand == nil {
		t.Fatalf("plan was not finalized: cid=%d qemu=%#v", plan.CID, plan.QEMUCommand)
	}
	if got := ControlStats(runtimeProvider.spec.Stats); got.QMPReadyAt.IsZero() || got.FilesReadyAt.IsZero() || got.SSHReadyAt.IsZero() {
		t.Fatalf("expected startup timers in stats: %#v", got)
	}
}
```

Add fakes in the same file:

```go
func testStarterPlan(t *testing.T) *Plan {
	t.Helper()
	return &Plan{
		Manifest: &manifest.Manifest{
			SSH: manifest.SSH{Argv: []string{"ssh"}},
		},
		Options:             Options{SSH: true},
		Paths:               RuntimePaths{QMPSocket: "qmp.sock", ControlSocket: "virtie.sock", SSHReadySocket: "ready.sock"},
		VirtioFSSocketPaths: []string{"fs.sock"},
	}
}

type fakeStarterHost struct {
	events       []string
	lock         *RuntimeLock
	qmp          qmpclient.Client
	prepareErr   error
	startRunsErr error
	waitQMPErr   error
}

func (h *fakeStarterHost) event(name string) {
	h.events = append(h.events, name)
}

func (h *fakeStarterHost) NewLifecycle(cancel context.CancelFunc) *Lifecycle {
	return NewSignalLifecycle(nil, cancel)
}

func (h *fakeStarterHost) AcquireRuntimeLock(RuntimeLockSpec) (*RuntimeLock, error) {
	h.event("lock")
	h.lock = &RuntimeLock{}
	return h.lock, nil
}

func (h *fakeStarterHost) AcquireCID(*manifest.Manifest, *SuspendState) (int, error) {
	h.event("cid")
	return 7, nil
}

func (h *fakeStarterHost) BuildQEMUCommand(*manifest.Manifest, int, bool) (*exec.Cmd, error) {
	h.event("qemu-command")
	return exec.Command("qemu-system-x86_64"), nil
}

func (h *fakeStarterHost) PrepareRuntimeState(*Plan) error {
	h.event("prepare")
	return h.prepareErr
}

func (h *fakeStarterHost) RemoveSocketPaths([]string) error {
	h.event("remove-sockets")
	return nil
}

func (h *fakeStarterHost) StartRuns(int, *manifest.Manifest) (executor.Group, error) {
	h.event("start-runs")
	return executor.NewGroup(), h.startRunsErr
}

func (h *fakeStarterHost) StartQEMU(*exec.Cmd) (*executor.Process, error) {
	h.event("start-qemu")
	return nil, nil
}

func (h *fakeStarterHost) InstallQMPShutdown(*executor.Process, qmpclient.Client) {
	h.event("install-qmp-shutdown")
}

func (h *fakeStarterHost) WaitForSockets(ctx context.Context, stage string, paths []string, watchers executor.Group) error {
	h.event("wait-sockets:" + stage)
	return nil
}

func (h *fakeStarterHost) WaitForQMP(context.Context, string, executor.Group) (qmpclient.Client, error) {
	h.event("wait-qmp")
	if h.qmp == nil {
		h.qmp = &fakeStarterQMP{}
	}
	return h.qmp, h.waitQMPErr
}

func (h *fakeStarterHost) RestoreRuntime(context.Context, *Plan, qmpclient.Client) error {
	h.event("restore")
	return nil
}

func (h *fakeStarterHost) WriteGuestFiles(context.Context, *Plan, *Stats, executor.Group) error {
	h.event("write-guest-files")
	return nil
}

func (h *fakeStarterHost) WriteBackGuestFiles(context.Context, *Plan, executor.Group) error {
	h.event("write-back-guest-files")
	return nil
}

func (h *fakeStarterHost) WaitForSSHReady(context.Context, string, executor.Group) error {
	h.event("wait-ssh-ready")
	return nil
}

func (h *fakeStarterHost) ShutdownDelay() time.Duration {
	return 0
}

func (h *fakeStarterHost) StatsOutput() io.Writer {
	return nil
}

type fakeStarterQMP struct{}

func (q *fakeStarterQMP) WithRaw(time.Duration, func(*rawQMP.Monitor) error) error { return nil }
func (q *fakeStarterQMP) RunRaw(time.Duration, string) error                       { return nil }
func (q *fakeStarterQMP) DeviceDelAndWait(time.Duration, string) error             { return nil }
func (q *fakeStarterQMP) Stop(time.Duration) error                                 { return nil }
func (q *fakeStarterQMP) Cont(time.Duration) error                                 { return nil }
func (q *fakeStarterQMP) QueryStatus(time.Duration) (string, error)                { return "running", nil }
func (q *fakeStarterQMP) Quit(time.Duration) error                                 { return nil }
func (q *fakeStarterQMP) MigrateToFile(time.Duration, string) error                { return nil }
func (q *fakeStarterQMP) MigrateIncoming(time.Duration, string) error              { return nil }
func (q *fakeStarterQMP) QueryMigrate(time.Duration) (string, error)               { return "completed", nil }
func (q *fakeStarterQMP) Disconnect() error                                        { return nil }

type fakeStarterRuntime struct {
	events  *[]string
	runtime *fakeStartedRuntime
	spec    RuntimeSpec
}

func (r *fakeStarterRuntime) New(spec RuntimeSpec) (RuntimeResult, error) {
	r.spec = spec
	r.runtime = &fakeStartedRuntime{events: r.events}
	if r.events != nil {
		*r.events = append(*r.events, "runtime-new")
	}
	return RuntimeResult{Runtime: r.runtime, ControlOptions: []control.RouterOption{control.WithHotplug(fakeUnsupportedHotplug{})}}, nil
}

func (r *fakeStarterRuntime) SuspendHandler(SuspendSpec) SuspendHandler {
	if r.events != nil {
		*r.events = append(*r.events, "runtime-suspend-handler")
	}
	return fakeSuspendHandler{events: r.events}
}

func (r *fakeStarterRuntime) WaitForeground(ForegroundSpec) func(context.Context, *Plan) error {
	if r.events != nil {
		*r.events = append(*r.events, "runtime-wait-foreground")
	}
	return func(context.Context, *Plan) error { return nil }
}

type fakeStartedRuntime struct {
	events *[]string
}

func (r *fakeStartedRuntime) SetReady() {
	if r.events != nil {
		*r.events = append(*r.events, "runtime-ready")
	}
}
func (r *fakeStartedRuntime) MarkSavedSuspend() {}
func (r *fakeStartedRuntime) SetWatchers(executor.Group) {}
func (r *fakeStartedRuntime) StartControl(context.Context, ...control.RouterOption) (*control.Server, error) {
	if r.events != nil {
		*r.events = append(*r.events, "runtime-control")
	}
	return nil, nil
}
func (r *fakeStartedRuntime) Wait(context.Context, WaitMode) error { return nil }
func (r *fakeStartedRuntime) Close() error                         { return nil }
func (r *fakeStartedRuntime) QMP() qmpclient.Client                { return &fakeStarterQMP{} }

type fakeSuspendHandler struct {
	events *[]string
}

func (h fakeSuspendHandler) Handle(context.Context, *SuspendCoordinator) error {
	if h.events != nil {
		*h.events = append(*h.events, "runtime-suspend-handle")
	}
	return nil
}

type fakeUnsupportedHotplug struct{}

func (fakeUnsupportedHotplug) Hotplug(context.Context, control.HotplugRequest) (control.HotplugResponse, error) {
	return control.HotplugResponse{}, nil
}
```

- [ ] **Step 3: Run starter tests and verify they fail**

Run:

```bash
cd virtie && go test ./internal/manager/launch -run 'TestLaunchPackageExportsStarterSurface|TestStarterFreshLaunchOrdersStartupAndReadiness' -count=1
```

Expected: FAIL because the starter types are missing.

- [ ] **Step 4: Implement starter type declarations**

Create `virtie/internal/manager/launch/starter.go` with package and imports:

```go
package launch

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)
```

Add type declarations:

```go
type Starter struct {
	Host    Host
	Runtime Runtime
}

var ErrSavedSuspendExit = errors.New("saved suspend requested")

func IsSavedSuspendExit(err error) bool {
	return errors.Is(err, ErrSavedSuspendExit)
}

type Host interface {
	NewLifecycle(context.CancelFunc) *Lifecycle
	AcquireRuntimeLock(RuntimeLockSpec) (*RuntimeLock, error)
	AcquireCID(*manifest.Manifest, *SuspendState) (int, error)
	BuildQEMUCommand(*manifest.Manifest, int, bool) (*exec.Cmd, error)
	PrepareRuntimeState(*Plan) error
	RemoveSocketPaths([]string) error
	StartRuns(int, *manifest.Manifest) (executor.Group, error)
	StartQEMU(*exec.Cmd) (*executor.Process, error)
	InstallQMPShutdown(*executor.Process, qmpclient.Client)
	WaitForSockets(context.Context, string, []string, executor.Group) error
	WaitForQMP(context.Context, string, executor.Group) (qmpclient.Client, error)
	RestoreRuntime(context.Context, *Plan, qmpclient.Client) error
	WriteGuestFiles(context.Context, *Plan, *Stats, executor.Group) error
	WriteBackGuestFiles(context.Context, *Plan, executor.Group) error
	WaitForSSHReady(context.Context, string, executor.Group) error
	ShutdownDelay() time.Duration
	StatsOutput() io.Writer
}

type Runtime interface {
	// New builds the runtime and optional control handlers from started VM state.
	New(RuntimeSpec) (RuntimeResult, error)

	// SuspendHandler builds the handler used for queued and signal-driven suspend requests.
	SuspendHandler(SuspendSpec) SuspendHandler

	// WaitForeground builds the foreground wait function stored on the runtime.
	WaitForeground(ForegroundSpec) func(context.Context, *Plan) error
}

type RuntimeSpec struct {
	Manifest        *manifest.Manifest
	Plan            *Plan
	Paths           RuntimePaths
	CID             int
	Stats           *Stats
	QMP             qmpclient.Client
	SuspendRequests *SuspendCoordinator
	Processes       *ProcessSet
	WaitForeground  func(context.Context, *Plan) error
	WriteBack        func(context.Context) error
	Cleanup          func() error
	CloseStats       func()
}

type RuntimeResult struct {
	Runtime        StartedRuntime
	ControlOptions []control.RouterOption
}

type StartedRuntime interface {
	SetReady()
	MarkSavedSuspend()
	SetWatchers(executor.Group)
	StartControl(context.Context, ...control.RouterOption) (*control.Server, error)
	Wait(context.Context, WaitMode) error
	Close() error
	QMP() qmpclient.Client
}

type SuspendSpec struct {
	Manifest    *manifest.Manifest
	Plan        *Plan
	QMP         qmpclient.Client
	CID         int
	WriteBackOnExit func() bool
}

type SuspendHandler interface {
	Handle(context.Context, *SuspendCoordinator) error
}

type ForegroundSpec struct {
	Plan           *Plan
	Stats          *Stats
	Runtime        func() StartedRuntime
	QMP            qmpclient.Client
	Lifecycle      *Lifecycle
	SuspendHandler SuspendHandler
	Processes      *ProcessSet
}
```

- [ ] **Step 5: Implement starter Start with ordering**

In `starter.go`, add:

```go
func (s Starter) Start(ctx context.Context, plan *Plan) (started StartedRuntime, err error) {
	if plan == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch plan is required")}
	}
	if s.Host == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch host is required")}
	}
	if s.Runtime == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch runtime is required")}
	}

	stats := NewStats(time.Now())
	launchCtx, cancelLaunch := context.WithCancel(ctx)
	lifecycle := s.Host.NewLifecycle(cancelLaunch)
	runtimeLock, err := s.Host.AcquireRuntimeLock(RuntimeLockSpec{
		Manifest:    plan.Manifest,
		ResumeState: plan.ResumeState,
		Lifecycle:   lifecycle,
		Cancel:      cancelLaunch,
		PID:         os.Getpid(),
	})
	if err != nil {
		return nil, &StageError{Stage: "preflight", Err: err}
	}

	processes := NewProcessSet()
	var qmp qmpclient.Client
	writeBackOnExit := false
	cleanupRuntime := func() error { return runtimeLock.Cleanup() }
	defer func() {
		if err == nil {
			return
		}
		if started != nil {
			if IsSavedSuspendExit(err) {
				started.MarkSavedSuspend()
			}
			err = errors.Join(err, started.Close())
			return
		}
		var cleanupErr error
		cleanupErr = errors.Join(cleanupErr, processes.Close(s.Host.ShutdownDelay()))
		cleanupErr = errors.Join(cleanupErr, cleanupRuntime())
		if qmp != nil {
			cleanupErr = errors.Join(cleanupErr, qmp.Disconnect())
		}
		cleanupErr = errors.Join(cleanupErr, s.Host.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles()))
		finalizeStats(stats, s.Host.StatsOutput())()
		err = errors.Join(err, cleanupErr)
	}()

	cid, err := s.Host.AcquireCID(plan.Manifest, plan.ResumeState)
	if err != nil {
		return nil, &StageError{Stage: "preflight", Err: err}
	}
	qemuCmd, err := s.Host.BuildQEMUCommand(plan.Manifest, cid, plan.ResumeState != nil)
	if err != nil {
		return nil, &StageError{Stage: "preflight", Err: err}
	}
	plan.CID = cid
	plan.QEMUCommand = qemuCmd
	if err := s.Host.PrepareRuntimeState(plan); err != nil {
		return nil, &StageError{Stage: "preflight", Err: err}
	}

	runProcesses, err := s.Host.StartRuns(plan.CID, plan.Manifest)
	if err != nil {
		return nil, err
	}
	processes.AddGroup(runProcesses)
	if len(plan.VirtioFSSocketPaths) > 0 {
		if err := s.Host.WaitForSockets(launchCtx, "virtiofs startup", plan.VirtioFSSocketPaths, processes.Watchers()); err != nil {
			return nil, err
		}
	}
	stats.Timer(TimerBootStarted, time.Now())
	qemu, err := s.Host.StartQEMU(plan.QEMUCommand)
	if err != nil {
		return nil, WrapFixedStage("vm startup")(err)
	}
	processes.SetQEMU(qemu)
	qmp, err = s.Host.WaitForQMP(launchCtx, plan.Paths.QMPSocket, processes.Watchers())
	if err != nil {
		return nil, err
	}
	qmp = qmpclient.Serialized(qmp)
	stats.Timer(TimerQMPReady, time.Now())
	s.Host.InstallQMPShutdown(qemu, qmp)
	if plan.ResumeState != nil {
		if err := s.Host.RestoreRuntime(launchCtx, plan, qmp); err != nil {
			return nil, err
		}
		writeBackOnExit = true
	}

	suspendHandler := s.Runtime.SuspendHandler(SuspendSpec{
		Manifest: plan.Manifest,
		Plan:     plan,
		QMP:      qmp,
		CID:      plan.CID,
		WriteBackOnExit: func() bool {
			return writeBackOnExit
		},
	})
	waitForeground := s.Runtime.WaitForeground(ForegroundSpec{
		Plan:           plan,
		Stats:          stats,
		Runtime:        func() StartedRuntime { return started },
		QMP:            qmp,
		Lifecycle:      lifecycle,
		SuspendHandler: suspendHandler,
		Processes:      processes,
	})
	result, err := s.Runtime.New(RuntimeSpec{
		Manifest:        plan.Manifest,
		Plan:            plan,
		Paths:           plan.Paths,
		CID:             plan.CID,
		Stats:           stats,
		QMP:             qmp,
		SuspendRequests: lifecycle.Suspend(),
		Processes:       processes,
		WaitForeground:  waitForeground,
		WriteBack: func(ctx context.Context) error {
			if !writeBackOnExit {
				return nil
			}
			return s.Host.WriteBackGuestFiles(ctx, plan, executor.Group{})
		},
		Cleanup: func() error {
			return errors.Join(s.Host.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles()), cleanupRuntime())
		},
		CloseStats: finalizeStats(stats, s.Host.StatsOutput()),
	})
	if err != nil {
		return nil, err
	}
	started = result.Runtime
	if started == nil {
		return nil, &StageError{Stage: "preflight", Err: errors.New("launch runtime returned nil runtime")}
	}
	started.SetReady()
	if _, err := started.StartControl(launchCtx, result.ControlOptions...); err != nil {
		return nil, WrapFixedStage("control startup")(err)
	}
	if err := HandleQueuedSuspend(launchCtx, lifecycle, suspendHandler.Handle); err != nil {
		return nil, err
	}
	if plan.ResumeState == nil {
		if err := s.Host.WriteGuestFiles(launchCtx, plan, stats, processes.Watchers()); err != nil {
			return nil, err
		}
		stats.Timer(TimerFilesReady, time.Now())
		if plan.Paths.SSHReadySocket != "" {
			if err := s.Host.WaitForSSHReady(launchCtx, plan.Paths.SSHReadySocket, processes.Watchers()); err != nil {
				return nil, err
			}
		}
		stats.Timer(TimerSSHReady, time.Now())
		writeBackOnExit = true
	}
	return started, nil
}
```

Keep any helper methods extracted from `Start` package-private and covered by the starter tests in this task.

- [ ] **Step 6: Run starter surface tests**

Run:

```bash
cd virtie && go test ./internal/manager/launch -run 'TestLaunchPackageExportsStarterSurface|TestStarterFreshLaunchOrdersStartupAndReadiness' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit starter surface**

```bash
git add virtie/internal/manager/launch/starter.go virtie/internal/manager/launch/starter_test.go virtie/internal/manager/launch/api_surface_test.go
git commit -m "virtie: Add launch starter surface" \
  -m "Introduce launch.Starter with launch.Host and launch.Runtime interfaces, plus initial ordering coverage for fresh launch startup." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager/launch -run 'TestLaunchPackageExportsStarterSurface|TestStarterFreshLaunchOrdersStartupAndReadiness' -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

## Task 4: Implement Manager Host And Runtime Providers

**Files:**
- Create: `virtie/internal/manager/launch_host.go`
- Create: `virtie/internal/manager/launch_runtime.go`
- Modify: `virtie/internal/manager/manager.go`
- Modify: `virtie/internal/manager/guest_agent.go`
- Modify: `virtie/internal/manager/runtime/dependencies.go`
- Modify: `virtie/internal/manager/runtime/concrete.go`
- Modify: `virtie/internal/manager/manager_test.go`

- [ ] **Step 1: Write provider compile test**

Add to `virtie/internal/manager/manager_test.go`:

```go
func TestManagerLaunchProvidersSatisfyLaunchStarterInterfaces(t *testing.T) {
	var _ launch.Host = launchHost{}
	var _ launch.Runtime = launchRuntime{}
}
```

- [ ] **Step 2: Run provider test and verify it fails**

Run:

```bash
cd virtie && go test ./internal/manager -run TestManagerLaunchProvidersSatisfyLaunchStarterInterfaces -count=1
```

Expected: FAIL because `launchHost` and `launchRuntime` do not exist.

- [ ] **Step 3: Implement launchHost**

Create `virtie/internal/manager/launch_host.go`:

```go
package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type launchHost struct {
	manager *manager
}

func (h launchHost) NewLifecycle(cancel context.CancelFunc) *launch.Lifecycle {
	return launch.NewSignalLifecycle(h.manager.signals, cancel)
}

func (h launchHost) AcquireRuntimeLock(spec launch.RuntimeLockSpec) (*launch.RuntimeLock, error) {
	return launch.AcquireRuntimeLock(spec)
}

func (h launchHost) AcquireCID(cfg *manifest.Manifest, resumeState *launch.SuspendState) (int, error) {
	return launch.AcquireCID(cfg, resumeState, h.manager.vsockCIDChecker)
}

func (h launchHost) BuildQEMUCommand(cfg *manifest.Manifest, cid int, restore bool) (*exec.Cmd, error) {
	return buildQEMUCommand(cfg, cid, restore)
}

func (h launchHost) PrepareRuntimeState(plan *launch.Plan) error {
	cfg := plan.Manifest
	for _, dir := range cfg.ResolvedPersistenceDirectories() {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	for _, path := range plan.RuntimeSocketCleanupFiles() {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	for _, path := range plan.ExternalVirtioFSSocketPaths {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("external virtiofs socket %q does not exist", path)
			}
			return fmt.Errorf("stat external virtiofs socket %q: %w", path, err)
		}
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("external virtiofs socket %q is not a socket", path)
		}
	}
	for _, path := range plan.VolumeImagePaths {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	if err := launch.RemoveSocketPaths(plan.RuntimeSocketCleanupFiles()); err != nil {
		return err
	}
	for _, volume := range plan.Volumes {
		if !volume.AutoCreate {
			continue
		}
		info, err := os.Stat(volume.ImagePath)
		if err == nil {
			if info.IsDir() {
				return fmt.Errorf("volume image %q is a directory", volume.ImagePath)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat volume image %q: %w", volume.ImagePath, err)
		}
		if h.manager.logger != nil {
			h.manager.logger.Info("creating volume image", "path", volume.ImagePath, "size_mib", volume.Size, "fs_type", volume.FSType)
		}
		if err := launch.CreateVolumeImage(volume); err != nil {
			return err
		}
	}
	return nil
}

func (h launchHost) RemoveSocketPaths(paths []string) error {
	return launch.RemoveSocketPaths(paths)
}

func (h launchHost) StartRuns(cid int, cfg *manifest.Manifest) (executor.Group, error) {
	return h.manager.startRuns(cid, cfg)
}

func (h launchHost) StartQEMU(cmd *exec.Cmd) (*executor.Process, error) {
	if h.manager.runner == nil {
		return nil, launch.WrapFixedStage("vm startup")(fmt.Errorf("qemu runner is not configured"))
	}
	return h.manager.runner.Start(cmd)
}

func (h launchHost) InstallQMPShutdown(qemu *executor.Process, client qmpclient.Client) {
	if qemu != nil && client != nil {
		qemu.SetShutdown(func() error {
			return client.Quit(h.manager.effectiveQMPQuitTimeout())
		})
	}
}

func (h launchHost) WaitForSockets(ctx context.Context, stage string, socketPaths []string, watchers executor.Group) error {
	return h.manager.waitForSockets(ctx, stage, socketPaths, watchers)
}

func (h launchHost) WaitForQMP(ctx context.Context, socketPath string, watchers executor.Group) (qmpclient.Client, error) {
	return h.manager.waitForQMP(ctx, socketPath, watchers)
}

func (h launchHost) RestoreRuntime(ctx context.Context, plan *launch.Plan, client qmpclient.Client) error {
	return h.manager.restoreLaunchRuntime(ctx, plan, client)
}

func (h launchHost) WriteGuestFiles(ctx context.Context, plan *launch.Plan, stats *launch.Stats, watchers executor.Group) error {
	return h.manager.writeGuestFiles(ctx, plan.Manifest, stats, watchers)
}

func (h launchHost) WriteBackGuestFiles(ctx context.Context, plan *launch.Plan, watchers executor.Group) error {
	return h.manager.writeBackGuestFiles(ctx, plan.Manifest, watchers)
}

func (h launchHost) WaitForSSHReady(ctx context.Context, socketPath string, watchers executor.Group) error {
	return h.manager.waitForSSHReady(ctx, socketPath, watchers)
}

func (h launchHost) ShutdownDelay() time.Duration {
	return h.manager.shutdownDelay
}

func (h launchHost) StatsOutput() io.Writer {
	return h.manager.outputWriter()
}
```

- [ ] **Step 4: Move saved-suspend sentinel ownership to launch**

In `virtie/internal/manager/manager.go`, replace the sentinel declaration:

```go
var errSavedSuspendExit = errors.New("saved suspend requested")

func isSavedSuspendExit(err error) bool {
	return errors.Is(err, errSavedSuspendExit)
}
```

with:

```go
var errSavedSuspendExit = launch.ErrSavedSuspendExit

func isSavedSuspendExit(err error) bool {
	return launch.IsSavedSuspendExit(err)
}
```

Keep `errSavedSuspendExit` as a manager-local alias so existing manager tests do not need to change in this task.

- [ ] **Step 5: Implement launchRuntime**

Create `virtie/internal/manager/launch_runtime.go`:

```go
package manager

import (
	"context"

	"github.com/shazow/agentspace/virtie/internal/executor"
	controlpkg "github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
)

type launchRuntime struct {
	manager *manager
}

func (r launchRuntime) New(spec launch.RuntimeSpec) (launch.RuntimeResult, error) {
	runtimeDeps := runtimepkg.Dependencies{
		QMPTimeout:       r.manager.effectiveQMPCommandTimeout(),
		Logger:           r.manager.logger,
		SavedSuspendExit: isSavedSuspendExit,
		CollectInfo: func(ctx context.Context, socketPath string, watchers executor.Group) (runtimepkg.GuestInfo, error) {
			info, err := r.manager.collectGuestInfo(ctx, socketPath, watchers)
			if err != nil {
				return runtimepkg.GuestInfo{}, err
			}
			return runtimepkg.GuestInfo{ProcessList: info.ProcessList}, nil
		},
	}
	core := runtimepkg.New(runtimepkg.RuntimeConfig{
		Manifest:        spec.Manifest,
		Plan:            spec.Plan,
		Paths:           spec.Paths,
		CID:             spec.CID,
		Stats:           spec.Stats,
		QMP:             spec.QMP,
		SuspendRequests: spec.SuspendRequests,
		Processes:       spec.Processes,
		ShutdownDelay:   r.manager.shutdownDelay,
		WaitForeground:  spec.WaitForeground,
		CloseHooks: runtimepkg.CloseHooks{
			WriteBack: spec.WriteBack,
			Cleanup:   spec.Cleanup,
			Stats:     spec.CloseStats,
		},
		Dependencies: runtimeDeps,
	})
	hotplugFeature := r.manager.hotplugFeature(spec.Manifest, core.QMP())
	return launch.RuntimeResult{
		Runtime:        core,
		ControlOptions: []controlpkg.RouterOption{controlpkg.WithHotplug(hotplugFeature)},
	}, nil
}

func (r launchRuntime) SuspendHandler(spec launch.SuspendSpec) launch.SuspendHandler {
	return launchSuspendHandlerAdapter{
		handler: newLaunchSuspendHandler(r.manager, spec.Manifest, spec.Plan.Paths.QMPSocket, spec.QMP, spec.CID, spec.Plan.Notifier, spec.WriteBackOnExit),
	}
}

func (r launchRuntime) WaitForeground(spec launch.ForegroundSpec) func(context.Context, *launch.Plan) error {
	return func(ctx context.Context, waitPlan *launch.Plan) error {
		return r.manager.waitForLaunchForeground(ctx, waitPlan, spec.Stats, spec.Runtime(), spec.QMP, spec.Lifecycle, spec.SuspendHandler, spec.Processes)
	}
}

type launchSuspendHandlerAdapter struct {
	handler *launchSuspendHandler
}

func (a launchSuspendHandlerAdapter) Handle(ctx context.Context, coordinator *launch.SuspendCoordinator) error {
	return handleSuspendRequest(ctx, coordinator, a.handler)
}
```

Change `manager.waitForLaunchForeground` to accept `launch.StartedRuntime` instead of `*runtimepkg.Core`. It only needs `SetWatchers`, which is part of the `StartedRuntime` interface.

- [ ] **Step 6: Run provider test**

Run:

```bash
cd virtie && go test ./internal/manager -run TestManagerLaunchProvidersSatisfyLaunchStarterInterfaces -count=1
```

Expected: PASS after import/signature fixes.

- [ ] **Step 7: Commit provider implementations**

```bash
git add virtie/internal/manager/launch_host.go virtie/internal/manager/launch_runtime.go virtie/internal/manager/manager_test.go
git commit -m "virtie: Add manager launch starter providers" \
  -m "Manager now has launchHost and launchRuntime providers that satisfy launch.Starter's host-effect and runtime-construction interfaces." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager -run TestManagerLaunchProvidersSatisfyLaunchStarterInterfaces -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

## Task 5: Replace `manager.startWithPlan` With `launch.Starter`

**Files:**
- Modify: `virtie/internal/manager/manager.go`
- Modify: `virtie/internal/manager/launcher.go`
- Modify: `virtie/internal/manager/manager_test.go`
- Modify: `virtie/internal/manager/runtime_test.go`

- [ ] **Step 1: Add manager integration regression for starter path**

Keep `TestLaunchRuntimeRegistersHotplugAtControlPeriphery` as the regression. It already starts the runtime through `manager.startWithPlan` and verifies that the control socket accepts hotplug after startup.

- [ ] **Step 2: Shrink `startWithPlan`**

Replace `manager.startWithPlan` body with:

```go
func (m *manager) startWithPlan(ctx context.Context, plan *launch.Plan) (runtime *runtimepkg.Core, err error) {
	started, err := launch.Starter{
		Host:    launchHost{manager: m},
		Runtime: launchRuntime{manager: m},
	}.Start(ctx, plan)
	if err != nil {
		return nil, err
	}
	core, ok := started.(*runtimepkg.Core)
	if !ok {
		_ = started.Close()
		return nil, &launch.StageError{Stage: "preflight", Err: fmt.Errorf("launch starter returned %T, not *runtime.Core", started)}
	}
	return core, nil
}
```

This keeps the internal `Launcher.Start` signature stable for this refactor.

- [ ] **Step 3: Remove startup code now owned by Starter**

From `manager.go`, remove startup orchestration blocks that are now duplicated in `launch.Starter`:

- Runtime lock acquisition inside `startWithPlan`.
- CID/QEMU finalization inside `startWithPlan`.
- Directory/socket/volume preparation inside `startWithPlan`.
- Process set creation inside `startWithPlan`.
- `startLaunchRuntime` call from `startWithPlan`.
- Runtime construction inside `startWithPlan`.
- Control startup and queued suspend handling inside `startWithPlan`.
- Guest file and SSH readiness handling inside `startWithPlan`.

Keep helper methods used by providers:

- `startRuns`
- `waitForQMP`
- `waitForLaunchSockets`
- `restoreLaunchRuntime`
- `waitForLaunchForeground`
- `runSSHSession`
- suspend handler helpers

Delete `startLaunchRuntime` only after its process/QMP logic is fully represented by `launch.Starter` and provider methods.

- [ ] **Step 4: Run manager startup tests**

Run:

```bash
cd virtie && go test ./internal/manager -run 'TestManagerLaunch|TestLaunchRuntimeRegistersHotplugAtControlPeriphery|TestManagerHotplug|TestManagerSuspend' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run full virtie tests**

Run:

```bash
cd virtie && go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit starter integration**

```bash
git add virtie/internal/manager/manager.go virtie/internal/manager/launcher.go virtie/internal/manager/manager_test.go virtie/internal/manager/runtime_test.go
git commit -m "virtie: Start launches through launch starter" \
  -m "manager.startWithPlan now delegates planned-launch startup to launch.Starter while manager providers retain concrete host and runtime wiring." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager -run 'TestManagerLaunch|TestLaunchRuntimeRegistersHotplugAtControlPeriphery|TestManagerHotplug|TestManagerSuspend' -count=1
- cd virtie && go test ./..." \
  -m "Assisted-by: codex:gpt-5"
```

## Task 6: Deepen Starter Tests And Remove Temporary Compatibility

**Files:**
- Modify: `virtie/internal/manager/launch/starter_test.go`
- Modify: `virtie/internal/manager/launch/starter.go`
- Modify: `virtie/internal/manager/manager.go`
- Modify: `virtie/internal/manager/launcher.go`
- Modify: `virtie/internal/manager/runtime/api_surface_test.go`
- Modify: `virtie/internal/manager/launch/api_surface_test.go`

- [ ] **Step 1: Add startup failure cleanup test**

In `starter_test.go`, add:

```go
func TestStarterStartupFailureCleansProcessesQMPAndSockets(t *testing.T) {
	plan := testStarterPlan(t)
	host := &fakeStarterHost{waitQMPErr: errors.New("qmp failed")}
	runtimeProvider := &fakeStarterRuntime{}
	starter := Starter{Host: host, Runtime: runtimeProvider}

	err := starter.Start(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "qmp failed") {
		t.Fatalf("expected qmp failure, got %v", err)
	}
	for _, want := range []string{"start-runs", "start-qemu", "wait-qmp", "remove-sockets"} {
		if !slices.Contains(host.events, want) {
			t.Fatalf("events %#v missing %q", host.events, want)
		}
	}
}
```

Add imports: `errors`, `slices`, `strings`.

- [ ] **Step 2: Add restore ordering test**

In `starter_test.go`, add:

```go
func TestStarterRestoresBeforeRuntimeConstruction(t *testing.T) {
	plan := testStarterPlan(t)
	plan.ResumeState = &SuspendState{VMStatePath: "vm.state", CID: 7}
	host := &fakeStarterHost{}
	runtimeProvider := &fakeStarterRuntime{}
	starter := Starter{Host: host, Runtime: runtimeProvider}

	if _, err := starter.Start(context.Background(), plan); err != nil {
		t.Fatalf("start: %v", err)
	}
	restoreIndex := slices.Index(host.events, "restore")
	runtimeIndex := slices.Index(host.events, "runtime-new")
	if restoreIndex < 0 || runtimeIndex < 0 || restoreIndex > runtimeIndex {
		t.Fatalf("restore should run before runtime construction, events=%#v", host.events)
	}
	if slices.Contains(host.events, "write-guest-files") {
		t.Fatalf("restore launch should skip fresh guest provisioning, events=%#v", host.events)
	}
}
```

- [ ] **Step 3: Add queued suspend ordering test**

In `starter_test.go`, add:

```go
func TestStarterDrainsQueuedSuspendBeforeGuestProvisioning(t *testing.T) {
	plan := testStarterPlan(t)
	host := &fakeStarterHost{}
	runtimeProvider := &fakeStarterRuntime{queueSuspend: true}
	starter := Starter{Host: host, Runtime: runtimeProvider}

	if _, err := starter.Start(context.Background(), plan); err != nil {
		t.Fatalf("start: %v", err)
	}
	suspendIndex := slices.Index(host.events, "runtime-suspend-handle")
	filesIndex := slices.Index(host.events, "write-guest-files")
	if suspendIndex < 0 || filesIndex < 0 || suspendIndex > filesIndex {
		t.Fatalf("queued suspend should drain before files, events=%#v", host.events)
	}
}
```

Implement `queueSuspend` in `fakeStarterRuntime.New` by calling `spec.SuspendRequests.Request()` before returning if the field is true.

- [ ] **Step 4: Remove temporary compatibility helpers**

Remove `launchStatsCloseHook` from `manager.go` once stats close handling is owned by `launch.Starter`.

Keep `Launcher.Plan` and `Launcher.Start` unchanged in this refactor. Do not change CLI or manifest behavior.

- [ ] **Step 5: Run starter and manager tests**

Run:

```bash
cd virtie && go test ./internal/manager/launch ./internal/manager -run 'TestStarter|TestManagerLaunch|TestLaunchRuntimeRegistersHotplugAtControlPeriphery' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit starter cleanup**

```bash
git add virtie/internal/manager/launch/starter.go virtie/internal/manager/launch/starter_test.go virtie/internal/manager/manager.go virtie/internal/manager/launcher.go virtie/internal/manager/runtime/api_surface_test.go virtie/internal/manager/launch/api_surface_test.go
git commit -m "virtie: Cover launch starter lifecycle ordering" \
  -m "Add focused launch.Starter tests for startup cleanup, restore ordering, and queued suspend ordering, then remove temporary compatibility helpers left from the migration." \
  -m "Validation performed:
- cd virtie && go test ./internal/manager/launch ./internal/manager -run 'TestStarter|TestManagerLaunch|TestLaunchRuntimeRegistersHotplugAtControlPeriphery' -count=1" \
  -m "Assisted-by: codex:gpt-5"
```

## Task 7: Documentation And Stale Surface Cleanup

**Files:**
- Modify: `.specs/virtie-manager-refactor.md`
- Modify: `docs/superpowers/specs/2026-06-13-launch-starter-design.md`
- Modify: `virtie/internal/manager/launch/api_surface_test.go`
- Modify: `virtie/internal/manager/runtime/api_surface_test.go`

- [ ] **Step 1: Update architecture record**

In `.specs/virtie-manager-refactor.md`, update package map bullets:

```md
- `LaunchWithOptions` composes planning, launch starter startup, foreground
  wait, and cleanup instead of owning lifecycle startup inline.
```

In the `manager/launch` section, add:

```md
- `Starter`, `Host`, and `Runtime` own planned-launch startup ordering,
  startup-failure cleanup, launch stats, and the seam between concrete host
  effects and runtime construction.
- `Stats`, `TimerEvent`, and `ProcessSet` live with launch startup because
  readiness timing and process ownership are startup concerns.
```

In Current Launch Flow, change steps 2-6 to describe `launch.Starter`.

- [ ] **Step 2: Update design spec drift**

In `docs/superpowers/specs/2026-06-13-launch-starter-design.md`, update any type field names or task outcomes that changed during implementation. Run:

```bash
rg -n 'runtimepkg\\.Stats|runtimepkg\\.ProcessSet|StatsFinalizer|StartupHost|StartupHooks|RuntimeWiring|Activation' docs/superpowers/specs/2026-06-13-launch-starter-design.md .specs/virtie-manager-refactor.md || true
```

Expected: no output.

- [ ] **Step 3: Run stale code scans**

Run:

```bash
rg -n 'runtimepkg\\.Stats|runtimepkg\\.NewStats|runtimepkg\\.StatsFinalizer|runtimepkg\\.ProcessSet|runtimepkg\\.NewProcessSet|MarkBootStarted|MarkQMPReady|MarkGuestAgentReady|MarkFilesReady|MarkSSHReady|MarkSSHAttempt|MarkSSHStarted|MarkCompleted' virtie/internal/manager || true
rg -n 'StartupHost|StartupHooks|RuntimeWiring|Activation' virtie/internal/manager docs/superpowers/specs .specs || true
```

Expected: no output.

- [ ] **Step 4: Run full validation**

Run:

```bash
cd virtie && go test ./...
git diff --check
```

Expected: PASS and no diff whitespace output.

- [ ] **Step 5: Commit docs and cleanup**

```bash
git add .specs/virtie-manager-refactor.md docs/superpowers/specs/2026-06-13-launch-starter-design.md virtie/internal/manager/launch/api_surface_test.go virtie/internal/manager/runtime/api_surface_test.go
git commit -m "virtie: Document launch starter topology" \
  -m "Update architecture notes and API surface checks after launch startup moves behind launch.Starter with launch-owned stats and process ownership." \
  -m "Validation performed:
- rg -n 'runtimepkg\\.Stats|runtimepkg\\.NewStats|runtimepkg\\.StatsFinalizer|runtimepkg\\.ProcessSet|runtimepkg\\.NewProcessSet|MarkBootStarted|MarkQMPReady|MarkGuestAgentReady|MarkFilesReady|MarkSSHReady|MarkSSHAttempt|MarkSSHStarted|MarkCompleted' virtie/internal/manager || true
- rg -n 'StartupHost|StartupHooks|RuntimeWiring|Activation' virtie/internal/manager docs/superpowers/specs .specs || true
- cd virtie && go test ./...
- git diff --check" \
  -m "Assisted-by: codex:gpt-5"
```

## Final Verification

- [ ] **Step 1: Run full Go suite**

```bash
cd virtie && go test ./...
```

Expected: all packages pass.

- [ ] **Step 2: Run stale API scans**

```bash
rg -n 'runtimepkg\\.Stats|runtimepkg\\.NewStats|runtimepkg\\.StatsFinalizer|runtimepkg\\.ProcessSet|runtimepkg\\.NewProcessSet|MarkBootStarted|MarkQMPReady|MarkGuestAgentReady|MarkFilesReady|MarkSSHReady|MarkSSHAttempt|MarkSSHStarted|MarkCompleted' virtie/internal/manager || true
rg -n 'StartupHost|StartupHooks|RuntimeWiring|Activation' virtie/internal/manager docs/superpowers/specs .specs || true
```

Expected: no stale live-code or current-spec references.

- [ ] **Step 3: Run whitespace check**

```bash
git diff --check
```

Expected: no output.

- [ ] **Step 4: Request final code review**

Ask a reviewer to inspect the implementation diff from `58cd475` through `HEAD`, focused on:

- startup ordering regressions,
- cleanup regressions,
- import cycles or shallow pass-through Modules,
- hotplug isolation regressions,
- stats/status behavior changes,
- missing tests for likely lifecycle bugs.

Address any findings with focused commits before offering branch completion options.
