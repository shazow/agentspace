# Migration Guide

This file tracks consumer-facing API changes and the steps needed to migrate
existing usage. Add a new dated section whenever a public command, Nix option,
flake output, manifest contract, or generated wrapper behavior changes.

## 2026-04-27: manifest writeFiles and guest-agent socket

### Who Is Affected

- Consumers that generate or validate virtie manifests directly.
- Nix consumers that want boot-time file injection into a fresh guest.

### What Changed

Generated manifests now include `qemu.guestAgent.socketPath`, resolved with the
same runtime-directory policy as QMP and virtiofs sockets. The sandbox module
also enables the in-guest QEMU guest agent service.

The manifest accepts an optional `writeFiles` map:

```json
{
  "writeFiles": {
    "/etc/example.conf": { "content": "YmFzZTY0IGJ5dGVz", "chown": "agent:users", "mode": "0640" },
    "/etc/from-host": { "path": "relative-or-absolute-host-path" }
  }
}
```

Exactly one of `content` or `path` is required for each entry. `content` must
already be base64-encoded. Relative host `path` values resolve against
`paths.workingDir`; guest target paths must be absolute.

Each entry may also set `chown` to a guest ownership string such as
`"agent:users"` and `mode` to a four-digit octal string such as `"0640"`.
When present, `virtie launch` applies ownership and then mode with the QEMU
guest agent after the file is written and closed. Chown and chmod failures are
fatal.

For Nix users, the equivalent option is:

```nix
agentspace.sandbox.writeFiles."/etc/example.conf".content = "YmFzZTY0IGJ5dGVz";
agentspace.sandbox.writeFiles."/etc/example.conf".chown = "agent:users";
agentspace.sandbox.writeFiles."/etc/example.conf".mode = "0640";
agentspace.sandbox.writeFiles."/etc/from-host".path = "relative-host-path";
```

`virtie launch` writes these files after QMP readiness and guest-agent ping, and
before SSH readiness. File injection runs only for fresh launches; restores via
`virtie launch --resume=auto` or `--resume=force` skip `writeFiles`.

### Migration Steps

No migration is required for consumers that do not set `writeFiles`. Direct
manifest producers should include `qemu.guestAgent.socketPath` when using
`writeFiles`.

## 2026-04-27: launch resume modes replace `virtie resume`

### Who Is Affected

- Direct users of `virtie resume`.
- Tooling that restores saved virtie sessions.

### What Changed

`virtie resume --manifest=MANIFEST` has been removed. Restores now use the
shared launch lifecycle:

```console
virtie launch --resume=force --manifest=MANIFEST
```

`virtie launch` also accepts `--resume=no` for fresh launch and `--resume=auto`
to restore when valid saved state exists, otherwise launch fresh. The default is
`--resume=no`.

### Migration Steps

Replace:

```console
virtie resume --manifest=MANIFEST
```

with:

```console
virtie launch --resume=force --manifest=MANIFEST
```

## 2026-04-27: saved-state suspend is the only suspend mode

### Who Is Affected

- Direct users of `virtie suspend`.
- Tooling that relied on live pause/resume state or the suspend exit flag.

### What Changed

`virtie suspend --manifest=MANIFEST` now saves QEMU migration state to disk,
exits the launch session, and writes saved suspend state. The old live
pause/resume behavior has been removed.

The external command now sends `SIGTSTP` to the launch process as a caught
control signal. It is not terminal/job-control suspend, and `SIGCONT` resume is
not supported.

The suspend exit flag has been removed. Restoring now goes through
`virtie launch --resume=force --manifest=MANIFEST`; it no longer signals a live
launch process or clears live paused state.

### Migration Steps

Replace suspend calls that passed the old exit flag with:

```console
virtie suspend --manifest=MANIFEST
```

Do not use `virtie launch --resume=force` unless saved suspend state exists.

## 2026-04-27: persistence state directory and resume workspace

### Who Is Affected

- Tooling that reads virtie PID, suspend, or VM state files directly.
- Direct users restoring a session from a different directory than
  the original launch.

### What Changed

Virtie runtime state now lives directly under the manifest persistence base
directory. With the default `agentspace.sandbox.persistence.basedir = ".agentspace"`,
files such as `<hostName>.pid`, `<hostName>.suspend.json`, and `<hostName>.vmstate`
are written to `.agentspace/` instead of `.agentspace/.virtie/`.

`virtie launch` also rewrites the copied runtime manifest so
`paths.workingDir` is the absolute launch workspace. `virtie launch --resume`
uses that manifest path to restore the original workspace share, even if it is
invoked from another current working directory.

### Migration Steps

Update tooling to read virtie state files from `<basedir>/` and continue
passing the copied runtime manifest, for example
`.agentspace/virtie-<hostName>.json`, to `virtie suspend` and
`virtie launch --resume=force`.

## 2026-04-26: sandbox launch command option

### Who Is Affected

- Consumers that want `nix run`/`mkLaunch` to start a specific in-guest command
  without passing command arguments at the shell each time.
- Tooling that inspects generated launch wrapper command lines.

### What Changed

`agentspace.sandbox.command` is now available as a string. The generated launch
wrapper passes it to `virtie launch` as the remote SSH command when the wrapper
is invoked without command arguments. Arguments supplied to the wrapper override
the configured command for that launch.

### Migration Steps

No migration is required. The default is `""`, preserving the previous
interactive session behavior.

## 2026-04-26: persistence base directory and disk suspend

### Who Is Affected

- Consumers relying on default persistence image paths in the repository root.
- Tooling that reads generated manifest or wrapper paths directly.
- Direct users of `virtie suspend`/`virtie resume`.

### What Changed

`agentspace.sandbox.persistence.basedir` defaults to `.agentspace`. Relative
`persistence.homeImage` and `persistence.storeOverlay` paths are now generated
under that base directory. The generated launch wrapper also copies its manifest
template to `<basedir>/virtie-<hostName>.json` at runtime and launches `virtie`
with that mutable workspace path.

`virtie suspend --manifest=MANIFEST` saves QEMU migration state under the
manifest persistence state directory, exits the launch session, and leaves
`virtie resume --manifest=MANIFEST` able to restore the saved VM when no live
launch PID is valid.

### Migration Steps

To keep root-level generated persistence files, set:

```nix
agentspace.sandbox.persistence.basedir = ".";
```

Tooling that invokes `virtie suspend` or `virtie resume` should use the runtime
manifest path generated by the launch wrapper, not the Nix-store template path.

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

At the time these commands were introduced, suspend state used the default
location:

```text
<workingDir>/.virtie/<hostName>.suspend.json
```

### Migration Steps

Update direct `virtie launch` calls:

```diff
- virtie launch "$manifest" -- "$@"
+ virtie launch --manifest="$manifest" -- "$@"
```

If tooling parses generated launch wrappers, update checks from the old
positional shape to the `--manifest=` shape.
