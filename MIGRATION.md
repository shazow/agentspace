# Migration Guide

This file tracks consumer-facing API changes and the steps needed to migrate
existing usage. Add a new dated section whenever a public command, Nix option,
flake output, manifest contract, or generated wrapper behavior changes.

## 2026-04-26: `virtie` manifest flag and suspend/resume commands

### Who Is Affected

- Consumers invoking `virtie launch` directly.
- Consumers inspecting generated launch wrapper scripts.
- Tooling that shells out to `virtie` with the old positional manifest form.

Normal `nix run` users that only call the generated launch wrapper do not need
to change their command line; the wrapper now passes the manifest flag for them.

### What Changed

`virtie launch` now requires an explicit `--manifest=MANIFEST` option instead
of a positional manifest path.

Before:

```console
virtie launch MANIFEST [-- <remote-cmd...>]
```

After:

```console
virtie launch --manifest=MANIFEST [-- <remote-cmd...>]
```

Two new lifecycle commands are available for the active QEMU process:

```console
virtie suspend --manifest=MANIFEST
virtie resume --manifest=MANIFEST
```

`suspend` and `resume` are keep-alive controls. They pause or continue the
still-running QEMU process through QMP and write/remove advisory state under:

```text
<workingDir>/.virtie/<hostName>.suspend.json
```

They are not full hibernation and restore commands.

### Migration Steps

Update direct `virtie launch` calls:

```diff
- virtie launch "$manifest" -- "$@"
+ virtie launch --manifest="$manifest" -- "$@"
```

If tooling parses generated launch wrappers, update checks from the old
positional shape to the `--manifest=` shape.
