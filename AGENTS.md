- Specification and progress is tracked in `.specs/*.md`
- When testing nix builds, clean up `./result` symlinks after completion with `unlink` command.

## Commits
- Consider committing major component changes separately if the changes work stand-alone. Order the commits incrementally so that they work as a unit.

Commit message style: `git commit -m "<component name>: <short description of changes>" -m "<Additional details about the changes worth noting>"`

Example resulting commit message:

```
virtie: Take over vsock allocation

VSock CID is no longer hardcoded in nix, instead it uses a placeholder which
gets overwritten by virtie.

Validation performed:
- ...

Closes #123
```
