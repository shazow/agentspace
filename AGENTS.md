# Agent Instructions

- Specification and progress is tracked in `.specs/*.md`
- When testing nix builds, clean up `./result` symlinks after completion with `unlink` command.
- This project should prioritize working on NixOS, but ideally also support macOS. Surface any concerns that may break macOS compatibility.
- Backward-incompatible changes are acceptable when justified. When changing the consumer API, document it in `MIGRATION.md`.
- When launching a VM, ensure we allocate less memory than is available, and choose an available CID (the default may be taken).

## Code Style

- Avoid making helpers that are only used once unless the helper code needs to be tested.
- When taking a string path input for reading/writing, convert it to an `io.Reader` or `io.Writer` in the `main` package early. Helpers and sub-packages and tests should prefer `io` interfaces.
- Avoid using the real filesystem in tests, prefer `io` or `io/fs` or `testing/fstest` when possible for virtual filesystem (such as `fs.File`, `fs.FS`, `fstest.MapFS`).

## Commits

- Consider committing major component changes separately if the changes work stand-alone. Order the commits incrementally so that they work as a unit.

Commit message style:
- First line: `<component name>: <short description of changes>`
- Following paragraph: `<Additional details about the changes worth noting>`
- When written the a coding agent, final line should include: `Assisted-by: AGENT_NAME:MODEL_VERSION`

Example resulting commit message:

```
virtie: Take over vsock allocation

VSock CID is no longer hardcoded in nix, instead it uses a placeholder which
gets overwritten by virtie.

Validation performed:
- ...

Closes #123

Assisted-by: codex:gpt-5.5-xhigh
```
