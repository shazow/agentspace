# Virtie Internal API Simplification Notes

**Status:** Review notes from the internal `go doc` audit.

## Scope

Reviewed every package returned by `cd virtie && go list ./internal/...` with
`go doc -all`. The review focused on whether the rendered documentation is
idiomatic Go documentation, whether exported internal names form sensible
package boundaries, and where the code can be simplified before adding more
surface-level comments.

Low-risk documentation cleanup applied during the audit: package comments were
added for `executor/executortest`, `hotplug`, `manager/control`,
`manager/launch`, `manager/runtime`, `qga`, `qmpclient`, and `units`.

## Exported Surface Snapshot

The counts below exclude `_test.go` files. "Missing comments" counts exported
top-level names and methods without declaration comments. Package comments are
now present for all internal packages.

| Package | Exported top-level | Exported methods | Missing comments | Review note |
| --- | ---: | ---: | ---: | --- |
| `manifest` | 100 | 42 | 141 | Real contract surface, but too broad to document casually. |
| `manager/launch` | 120 | 16 | 135 | Largest accidental API; parent `manager` is the only production consumer. |
| `manager/runtime` | 41 | 41 | 82 | Runtime boundary is real, helper/action types are wider than needed. |
| `manager/control` | 43 | 9 | 52 | Protocol boundary is real, but router/server internals leak. |
| `manager` | 26 | 19 | 41 | CLI-facing facade is reasonable; partial lifecycle APIs need a policy decision. |
| `sshtools` | 29 | 13 | 42 | Useful utility package, but key provisioning and retry output are separate concerns. |
| `executor` | 14 | 26 | 10 | Mostly idiomatic and cohesive. |
| `qmpclient` | 12 | 27 | 39 | Real protocol package; `Client` interface is broad. |
| `hotplug` | 23 | 9 | 32 | Feature implementation plus type aliases; build-tag boundary complicates API. |
| `executor/executortest` | 9 | 21 | 3 | Test-only support package; surface is acceptable if it stays test-focused. |
| `qga` | 17 | 10 | 27 | Real protocol package; `Client` interface is broad. |
| `balloon` | 9 | 6 | 15 | Optional feature implementation still re-exports always-built config helpers. |
| `hotplugtypes` | 14 | 0 | 14 | Always-built data package; useful but undocumented. |
| `balloontypes` | 8 | 0 | 8 | Always-built data package; useful but undocumented. |
| `manifest/tagged` | 6 | 0 | 0 | Small, well-documented, and reusable. |
| `readiness` | 3 | 0 | 0 | Small, well-documented, and reusable. |
| `units` | 1 | 2 | 3 | Tiny and useful; add declaration comments if it remains exported. |

The important conclusion is not "add 600 comments." For internal packages, the
better sequence is to shrink or close accidental APIs first, then document the
small set of names that remain exported.

## Package-by-Package Notes

### `balloon`

`go doc` now starts with a useful package overview. The remaining exported
surface mixes feature behavior (`AppendQEMUArgs`, `ControllerTask`,
`MonitorSession`) with aliases and wrappers for `balloontypes`.

Opportunity: remove `Device`, `ControllerConfig`, `ApplyDefaults`,
`ValidateController`, and `CloneDevice` from `balloon` if callers can import
`balloontypes` directly. Current source search found no non-package callers of
those aliases. That would leave `balloon` as the optional runtime/QEMU feature
package and `balloontypes` as the always-built config package.

### `balloontypes`

This package is a useful build-tag-neutral home for balloon configuration and
defaults. The API is small enough to keep, but declaration comments should be
added after deciding whether the constants need to be exported.

Opportunity: if only `manifest` and `runtime` need the defaults, consider
making default constants private and exposing behavior through `ApplyDefaults`
and `ValidateController`.

### `executor`

This is one of the cleaner packages. It has a clear package comment, a cohesive
model (`Runner`, `Process`, `Group`, `Renderer`), and exported interfaces that
describe real seams. The missing comments are mostly simple methods like
`Wait`, `Kill`, `Signal`, `Name`, and `PID`.

Opportunity: document the small remaining exported method set. Avoid replacing
`executor.Group` with `sync.WaitGroup`; it tracks process identity, reverse
shutdown ordering, and first-exit polling, which `sync.WaitGroup` does not.

### `executor/executortest`

Package comment added. The package is intentionally test support and should not
be judged like production API, but it is imported by multiple packages and has a
reasonable purpose.

Opportunity: keep it as the single process/runner fake. Avoid adding local fake
process implementations in manager, launch, qga, or qmp tests.

### `hotplug`

Package comment added. The package combines three things: build-tagged feature
implementation, aliases to `hotplugtypes`, and helper functions for state file
I/O. The alias layer makes `go doc` look larger than the actual implementation
surface.

Opportunity: make the implementation package expose only `Runtime` plus the
small dependency interfaces it needs. Have manager tests and manifest code use
`hotplugtypes` directly for data construction. Keep state-file helpers in
`hotplugtypes` unless they truly require the live feature implementation.

### `hotplugtypes`

This package is useful because hotplug data needs to exist even when the
`virtie_no_hotplug` tag removes the runtime implementation. The rendered docs
are easy to scan but lack declaration comments.

Opportunity: keep this package as a data package. Consider grouping state-file
helpers around `State`, for example `State.Path(dir, id)` is not worth it, but
renaming helpers to make the package prefix read well may help:
`hotplugtypes.StatePath`, `hotplugtypes.ReadState`, `hotplugtypes.WriteState`
already read acceptably.

### `manager`

The package comment is good and the CLI-facing facade makes sense. `Launch`,
`Suspend`, `Hotplug`, `DefaultConfig`, and `NewLauncher` are the right level for
`main`. The aliases for `ResumeMode`, `LaunchOptions`, and `WaitMode` are
acceptable as facade conveniences.

Opportunity: resolve the existing policy question around `Launcher.Plan` and
`Launcher.Start`. If they are only test seams, make the tests exercise a smaller
unexported helper or keep them in `manager` with explicit comments explaining
why partial lifecycle entrypoints are supported.

### `manager/control`

Package comment added. The protocol types are a real boundary: request and
response structs, `RuntimeState`, `RuntimeStats`, `RPCError`, `ErrorCode`,
`Client`, `Server`, and runtime capability interfaces all make sense as a
typed control socket API.

Opportunity: unexport `Router.Handle`. It is exported but takes and returns
unexported envelope types, so it renders poorly in `go doc` and is only called
inside the package. Also consider hiding `Server.Handler` and `Server.Logger`
behind constructors or options; exported fields make the server easier to
misconfigure than necessary.

### `manager/launch`

Package comment added. This is the main maintainability pressure point. The
package has 136 exported names, and same-package tests mean most exports are
not required for tests. The parent `manager` package is the only production
consumer, and many `launch.X` names are called once from manager.

Opportunity: choose one of two directions:

1. Make `launch` own a larger orchestration step and expose a smaller API, such
   as `BuildPlan`, `Setup`, `StartProcesses`, `ProvisionGuest`, and
   `WaitForeground`. Helper structs like `QMPWait`, `GuestAgentWait`,
   `SSHReadyWait`, `RuntimeRestore`, `RuntimeSuspendSave`, and
   `SSHSession` can then become private implementation details.
2. If the parent manager should remain the orchestrator, move the one-call
   launch helpers back into `manager` or make them methods on a small private
   manager-owned launch helper. This reduces the cross-package API without
   reintroducing wrapper-only abstractions.

The first direction keeps components isolated and replaceable; the second
direction keeps call flow explicit. Either is better than a subpackage with a
large exported vocabulary that only one parent package speaks.

### `manager/runtime`

Package comment added. `Runtime` is a real boundary because it backs the
control socket and owns live QMP/process state. The helper API is broader than
that boundary requires: `CloseActions`, `StartupFailureActions`,
`ShutdownResources`, `ProcessSet`, `Task`, `TaskGroup`, `State`, and `Stats`
are mostly construction and teardown internals for manager.

Opportunities:

- Keep `Runtime`, `RuntimeConfig`, `Dependencies`, `CloseHooks`, and control
  methods as the visible boundary, then make teardown/action helpers private.
- Replace or narrow `Task`/`TaskGroup`. The current implementation is a small
  custom cancel-and-join group. With Go 1.25, `sync.WaitGroup.Go`,
  `context.WithCancel`, `sync.Once`, and `errors.Join` can express most of the
  same behavior without an exported task type.
- Consider whether `ProcessSet` needs to be exported. Manager can pass process
  ownership at construction time and receive only watcher snapshots or close
  hooks, leaving the set private to runtime.

### `manifest`

The package comment is good, but `go doc` is very large because the package
contains both raw input schema and resolved runtime contract. The size is
partly legitimate: this is the internal contract emitted by Nix and consumed by
virtie. The current pointer-heavy input structs are also justified where
omission matters; that matches the project style guidance.

Opportunities:

- Split by lifecycle rather than by file size if this keeps improving:
  `manifest/input` for TOML/JSON input structs and tagged union decoding,
  `manifest` for the resolved runtime contract, or `manifest/schema` plus
  `manifest/resolved`. This is only worth doing if it reduces imports and
  defaulting complexity.
- Keep pointer fields in parse/input structs where omission matters, but lower
  earlier into value-oriented resolved structs. If repeated `*bool`, `*int`,
  and `*string` handling grows, consider a small local `Optional[T]` only if it
  improves TOML/JSON decoding and error messages without adding dependency
  cost.
- Add declaration comments for exported schema structs that are effectively
  part of the generated manifest contract. These comments should describe
  contract semantics, not restate field names.

### `manifest/tagged`

This package is small, documented, and cohesive. It is a good example of the
API shape the other helper packages should move toward.

Opportunity: none beyond keeping it focused on tagged union decoding.

### `qga`

Package comment added. The package is a real protocol adapter, but `Client` is
fat: file operations, exec, readiness ping, and disconnect all live on one
interface. This makes small consumers depend on the full QGA surface and
inflates test fakes.

Opportunity: prefer a concrete connection type plus small consumer-side
interfaces. Examples: `Pinger`, `FileClient`, `ExecClient`, and
`Disconnecter`, or keep interfaces unexported at call sites. `ReadFile`,
`WriteFile`, and `RunCommandStatus` already point in this direction because
they each need only a subset of behavior.

### `qmpclient`

Package comment added. The package is also a real protocol adapter. The broad
`Client` interface mixes raw command access, lifecycle commands, migration,
device deletion, status queries, and disconnect. `Serialized` is important and
should stay idempotent.

Opportunity: either expose a concrete `Conn` and keep small interfaces at call
sites, or split narrow capability interfaces inside the package:
`RawRunner`, `Lifecycle`, `Migration`, `DeviceController`, and
`Disconnecter`. This would let runtime/hotplug/balloon depend on only the QMP
behavior they need.

### `readiness`

This package is already small and idiomatic. The `go doc` output is easy to
understand and all exported functions have comments.

Opportunity: none.

### `sshtools`

The package comment is good and the package provides useful reusable SSH
helpers. The surface mixes argument construction, retry classification, retry
output buffering, key generation, and authorized-key installation planning.

Opportunity: if it grows further, split around behavior: command construction,
retry diagnostics, and key provisioning. For now, adding declaration comments
may be enough because manager and launch use only a small portion of the
package.

### `units`

Package comment added. The API is tiny and useful.

Opportunity: add comments to `MiB`, `Bytes`, and `Int`, or keep `MiB` exported
and make conversion helpers package-private if callers can use explicit casts
in the few places where they need raw values.

## Prioritized Simplification Opportunities

1. Shrink `manager/launch` exported surface. This is the largest source of
   non-idiomatic docs and inter-package vocabulary. Start by deciding whether
   launch owns orchestration or manager owns orchestration.
2. Close obvious leaks in `manager/control`: unexport `Router.Handle`, hide
   mutable `Server` fields, and document the protocol structs that remain.
3. Narrow `manager/runtime` to `Runtime` plus construction/configuration.
   Make process/task/close/startup helper types private unless another package
   truly needs to name them.
4. Split broad protocol clients by capability or move to concrete clients plus
   small local interfaces in `qga` and `qmpclient`.
5. Remove optional-feature alias layers where always-built type packages are
   already imported directly (`balloon` -> `balloontypes`, `hotplug` ->
   `hotplugtypes`).
6. Treat declaration comments as the final cleanup pass after API reduction.
   Adding comments before shrinking the surface would make accidental APIs look
   more intentional.

## Verification Commands Used During Audit

- `cd virtie && go list ./internal/...`
- `cd virtie && go doc -all <package>` for each internal package
- AST scan of non-test Go files for exported declarations and missing comments
- `rg` searches for package imports and cross-package exported-name usage
