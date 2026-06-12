# Virtie Optional Feature Build Tag Recommendation

**Status:** Recommendation.

## Question

Should virtie keep the `virtie_no_hotplug` and `virtie_no_balloon` build tags,
or remove them to simplify maintenance?

## Recommendation

Keep the current `virtie_no_*` build tags for now, but treat them as a bounded
architecture forcing function rather than as a feature family to expand.

The tags are useful because they make accidental coupling fail at compile time.
`manifest` and most of `manager` can depend on always-built data packages such
as `hotplugtypes` and `balloontypes`, while the live runtime implementation can
remain isolated in `hotplug` and `balloon`. That is valuable while these
features are still settling, because a future rewrite can replace the runtime
implementation without dragging the manifest contract, control protocol, or
manager facade through the same change.

The tags should not become the main abstraction mechanism. Their job is only to
prove that optional feature implementation packages are not required by the
core binary. The primary maintainability strategy should be package boundaries,
small consumer-side interfaces, and focused API-surface tests.

## Strategy

Use build tags as a temporary pressure test for optional runtime implementation
boundaries:

- Keep `hotplugtypes` and `balloontypes` always built. These packages carry
  configuration, manifest, and state contracts that should remain available
  regardless of whether the live runtime feature is compiled.
- Keep `hotplug` and `balloon` as implementation packages. They should expose
  runtime behavior, not re-export their data packages.
- Keep disabled-tag files small and boring. A disabled build should return a
  clear unsupported-feature error; it should not duplicate feature logic.
- Keep the build-tag test matrix in place while these boundaries are being
  cleaned up:
  - `go test ./...`
  - `go test -tags virtie_no_hotplug ./...`
  - `go test -tags virtie_no_balloon ./...`
  - `go test -tags 'virtie_no_hotplug virtie_no_balloon' ./...`
- Do not add new `virtie_no_*` tags unless a feature has a concrete dependency,
  platform, binary-size, or rewrite-isolation reason.
- Revisit removal after the runtime package boundaries stabilize. If the tags
  stop catching real coupling problems, replace them with explicit architecture
  tests that assert allowed imports.

## Current Code Weight

Approximate line counts from the current working tree:

| Category | Lines | Notes |
| --- | ---: | --- |
| Build-tagged production feature code | 1,291 | Mostly actual hotplug and balloon implementation that would still exist if the tags were removed. |
| Disabled-build shims | 76 | Small unsupported-feature adapters for disabled hotplug/balloon builds. |
| Build-tag-specific tests | 1,390 | Feature tests plus disabled-build behavior tests and API-surface tests. |
| Total build-tagged Go lines | 2,757 | About 9% of all Go lines in `virtie/` in this snapshot. |

The direct code cost of keeping the tags is not the whole 2,757 lines. Most of
the feature implementation and tests would still be needed without tags. The
incremental code cost is closer to the disabled shims plus some test-matrix and
mental-model overhead: roughly 76 lines of production shim code today, plus the
need to keep four Go test variants green.

The more important maintenance cost is cognitive:

- Readers must understand why `hotplugtypes` and `balloontypes` exist beside
  `hotplug` and `balloon`.
- Feature wiring has two build-tag paths in `manager` and `manager/runtime`.
- Any shared behavior accidentally placed in a tagged implementation package
  breaks disabled builds and has to be moved to an always-built package.

That cognitive cost is acceptable right now because it supports the desired
isolation. It becomes unacceptable if disabled builds gain complex alternate
logic, if more optional tags are added casually, or if the tag matrix regularly
breaks without revealing meaningful coupling.

## Removal Criteria

Remove the tags if most of these become true:

- Disabled binaries are not useful as artifacts or as local development checks.
- The boundaries are stable enough that import/API tests can enforce them.
- The disabled shims start needing non-trivial behavior.
- The test matrix becomes a recurring maintenance tax without catching
  boundary regressions.
- The team consistently treats the tags as incidental complexity rather than
  architecture documentation.

If removed, keep the same package strategy:

- `hotplugtypes` and `balloontypes` remain always-built data packages.
- `hotplug` and `balloon` remain runtime implementation packages.
- Add architecture tests that prevent broad imports of runtime implementation
  packages from manifest/defaulting code.
- Keep unsupported-feature errors where runtime capabilities can be absent for
  other reasons, such as older launch processes or control-socket capability
  negotiation.

## Decision

Keep `virtie_no_hotplug` and `virtie_no_balloon` in the near term.

The tags are currently a useful compile-time forcing function with a modest
direct shim cost. They should stay only as long as they enforce isolation
without growing parallel implementations. The project should prefer
always-built contract packages, narrow runtime adapters, and explicit
architecture tests over adding more build-tag variants.
