# Agent Instructions

- Specification and progress is tracked in `.specs/*.md`
- When testing nix builds, clean up `./result` symlinks after completion with `unlink` command.
- This project should prioritize working on NixOS, but ideally also support macOS. Surface any concerns that may break macOS compatibility.

## Code Style

- Avoid making helpers that are only used once unless the helper code needs to be tested.

## Commits
- Consider committing major component changes separately if the changes work stand-alone. Order the commits incrementally so that they work as a unit.

Commit message style: `git commit -m "<component name>: <short description of changes>" -m "<Additional details about the changes worth noting>"`

When written the a coding agent, final line should include: `Assisted-by: AGENT_NAME:MODEL_VERSION [TOOL1] [TOOL2]`

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
