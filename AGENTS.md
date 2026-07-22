# Agent Instructions

## Layout

- The remote origin project is https://github.com/shazow/agentspace
- Project root contains the Nix flake and VM/check definitions.
- Summary: The agentspace flake builds the VM image, wraps helper scripts, and produces `manifest.toml` for the standalone `virtle` runtime. Agentspace owns the Nix module and generated manifest surfaces; Virtle owns its command line.

## Process

- When testing nix builds, clean up `./result` symlinks after completion with `unlink` command.
- This project should prioritize working on NixOS, but ideally also support macOS. Surface any concerns that may break macOS compatibility.
- Backward-incompatible changes are acceptable when justified. When changing the consumer API, document it in `MIGRATION.md`.
- When launching a VM, ensure we allocate less memory than is available, and choose an available CID (the default may be taken).
- When debugging VM boot failures, use `mkSandbox { quiet = false; ... }` in agentspace or `kernel.serial = "print"` in the virtle manifest.

## Code Style

- Avoid making helpers that are only used once unless the helper code needs to be tested.
- Keep pointer fields in parse/input structs when omission matters; lower early into value-oriented runtime structs with explicit defaults.

### Testing

- Tests should be evergreen and focus on exercising affirmative functionality.
- When writing a test to validate the removal of some functionality, note that it is temporary. Mention the tested scenario in the validation report but remove temporary tests before committing.
- Name temporary tests clearly or add a short `XXX` comment with the condition for removing them.
- Avoid using timed sleeps in tests, prefer signal-based control flows whenever possible. If sleep is the only way, then use a module-global constant to have uniform values for slow and fast sleep durations.

## Commits

- Run `nix fmt` before committing nix changes.
- Consider committing major component changes separately if the changes work stand-alone. Order the commits incrementally so that they work as a unit.

Commit message style:
- First line: `<component name>: <short description of changes>`
- Following paragraph: `<Additional details about the changes worth noting>`
- If we're completing a specific issue number, mention it: `Closes #234`
- Final line should include: `Assisted-by: HARNESS:MODEL_VERSION` (such as `Assisted-by: codex:gpt-5.6-sol`)

Example resulting commit message:

```
sandbox: Configure runtime vsock allocation

VSock CID is no longer hardcoded in nix, instead it uses a placeholder which
the runtime launcher overwrites.

Validation performed:
- ...

Fixes #123
Closes #234

Assisted-by: codex:gpt-5.5-xhigh
```
