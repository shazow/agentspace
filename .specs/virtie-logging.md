# Virtie Logging

Structured logging design pattern for `virtie`.

**Status**: Completed

## Goals

Use stdlib `log/slog` for runtime diagnostics while keeping logging policy owned by the command entrypoint.

- Each package owns a package-local logger that defaults to discard.
- Packages expose `SetLogger(*slog.Logger)` so callers can choose output, formatting, level, attributes, and package inclusion.
- Subpackages depend only on `log/slog`, not on a shared internal logging package.
- `main` decides verbosity and which package loggers are enabled.
- Major structs may hold a `*slog.Logger` for tests or local customization.
- Constructors initialize struct loggers, so internal methods can log directly without nil-guard wrapper methods.

Out of scope:

- A project-wide logging abstraction.
- Per-package handler construction outside the command entrypoint.
- Defensive nil handling for internal struct logger fields when constructors control initialization.
- Tests that only assert logger setup wiring.

Acceptance criteria:

- [x] `virtie` uses stdlib `log/slog` for structured runtime logs.
- [x] Package loggers default to `slog.New(slog.DiscardHandler)`.
- [x] Package `SetLogger` functions accept `*slog.Logger`.
- [x] The command entrypoint owns handler construction and verbosity policy.
- [x] Internal structs that need logger customization store `*slog.Logger` fields initialized by constructors or explicit test fixtures.
- [x] Internal logging call sites use struct/package loggers directly instead of `log() *slog.Logger` gate wrappers.

## Progress

- [x] Added package-local `logger.go` files for `internal/manager` and `internal/balloon`.
- [x] Removed the shared `internal/logging` package.
- [x] Configured `virtie launch -v` to enable manager logs and `-vv` to include balloon logs.
- [x] Updated tests to provide discard loggers for direct struct fixtures that bypass constructors.
- [x] Removed logger setup tests in favor of structural coverage from package compilation and runtime tests.

## Appendix

The package-local logger pattern is intentionally small:

```go
package example

import "log/slog"

var logger = slog.New(slog.DiscardHandler)

func SetLogger(l *slog.Logger) {
	logger = l
}
```

The command entrypoint owns logging policy:

```go
baseLogger := slog.New(slog.NewTextHandler(os.Stderr, nil))
discardLogger := slog.New(slog.DiscardHandler)

manager.SetLogger(discardLogger)
balloon.SetLogger(discardLogger)

if verbosity > 0 {
	manager.SetLogger(baseLogger.With("package", "manager"))
}
if verbosity > 1 {
	balloon.SetLogger(baseLogger.With("package", "balloon"))
}
```

Structs that need logger customization should receive a logger through their constructor or through explicit test fixtures:

```go
type controller struct {
	Logger *slog.Logger
}

func newController() *controller {
	return &controller{
		Logger: logger,
	}
}

func (c *controller) Run(ctx context.Context) error {
	c.Logger.Info("controller started")
	return nil
}
```

Direct struct literals in tests that bypass constructors must set `slog.New(slog.DiscardHandler)` or a buffer-backed logger when assertions need log output. Production code should prefer constructors so nil logger fields are not part of the normal control flow.
