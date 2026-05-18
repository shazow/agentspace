# Agent Instructions

- Specification and progress is tracked in `.specs/*.md`
- When testing nix builds, clean up `./result` symlinks after completion with `unlink` command.
- This project should prioritize working on NixOS, but ideally also support macOS. Surface any concerns that may break macOS compatibility.
- Backward-incompatible changes are acceptable when justified. When changing the consumer API, document it in `MIGRATION.md`.
- When launching a VM, ensure we allocate less memory than is available, and choose an available CID (the default may be taken).
- When debugging QEMU serial console output, add `-serial file:console.log` to pipe the output.

## Code Style

- Avoid making helpers that are only used once unless the helper code needs to be tested.
- When taking a string path input for reading/writing, convert it to an `io.Reader` or `io.Writer` in the `main` package early. Helpers and sub-packages and tests should prefer `io` interfaces.

### Testing

- Tests should be evergreen and focus on exercising affirmative functionality.
- When writing a test to validate the removal of some functionality, note that it is temporary. Mention the tested scenario in the validation report but remove temporary tests before committing.
- Name temporary tests clearly or add a short `XXX` comment with the condition for removing them.
- Avoid using the real filesystem in tests, prefer `io` or `io/fs` or `testing/fstest` when possible for virtual filesystem (such as `fs.File`, `fs.FS`, `fstest.MapFS`).
- Avoid using timed sleeps in tests, prefer signal-based control flows whenever possible. If sleep is the only way, then use a module-global constant to have uniform values for slow and fast sleep durations.

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
