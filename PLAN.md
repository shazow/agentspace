# Plan: Fix virtiofsd file handle warnings

## Context

Issue #81 is about noisy `virtiofsd` warnings from default managed agentspace
launches. The wrapper currently tries to avoid file-handle warnings by selecting
`--inode-file-handles=prefer` when `CAP_DAC_READ_SEARCH` appears effective.
That is not sufficient: file-handle support can still fail at runtime depending
on the host filesystem and namespace setup, producing warnings like:

```text
Failed to open file handle for the root node: Operation not permitted (os error 1)
File handles do not appear safe to use, disabling file handles altogether
```

The existing uid/gid remapping approach is also not safe as a default because a
single root mapping changes guest-visible ownership semantics for shared paths.

## Fix

- Default managed wrappers to `--inode-file-handles=never`.
- Keep `microvm.virtiofsd.inodeFileHandles` as the explicit opt-in for users who
  know file handles are safe in their environment.
- Keep dynamic nofile handling from `ulimit -Hn`.
- Keep virtiofsd's default namespace sandbox and do not add default uid/gid maps.
- Update the migration note to describe the final wrapper behavior.

## Tests

- Keep positive manifest assertions for dynamic nofile handling and the default
  `--inode-file-handles=never`.
- Add a positive opt-in check that
  `microvm.virtiofsd.inodeFileHandles = "prefer"` appears in the generated
  wrapper.
- Remove outdated negative assertions for temporary removed-code validation.

## Validation

Run:

```sh
nix fmt
nix build .#checks.x86_64-linux.virtie-manifest-contract
test -L result && unlink result || true
```
