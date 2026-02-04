# Agent Prompt: Recursive Container Sandbox

## Task

Test that recursive container nesting works. The configuration has been updated but not yet tested from a fresh container.

## Tests to Run

1. **Verify capabilities are granted:**
   ```bash
   cat /proc/self/status | grep Cap
   # CapEff should be non-zero (e.g., 00000000a80c25fb)
   ```

2. **Test Nix build inside container:**
   ```bash
   nix build .#container
   ```

3. **Test nested container launch:**
   ```bash
   nix develop
   # Should show: "🛡️ Nested container detected. Using default runtime (runc)."
   ```

4. **Inside nested container, verify nesting detection:**
   ```bash
   cat /run/.containerenv  # Should exist
   ```

## Recent Changes Made

The following fixes were applied to enable recursive containers:

1. **flake.nix line ~138**: Added `shadow` package for `newuidmap`/`newgidmap` (required for rootless podman)
2. **flake.nix line ~265**: Added `--user=root` to podman run (enables capabilities to work with `--userns=keep-id`)
3. **.devcontainer/devcontainer.json**: Added `runArgs` with capabilities (backup for VS Code devcontainer usage)

## If Tests Fail

Check these areas in flake.nix:
- **Lines ~134-150**: Package includes (shadow, podman, fuse-overlayfs, storage/containers.conf)
- **Lines ~186-188**: /etc/subuid and /etc/subgid setup
- **Lines ~194-196**: /run/.containerenv marker
- **Lines ~264-271**: Capability grants and --user=root flag

## Success Criteria

- `CapEff` is non-zero when running `cat /proc/self/status | grep Cap`
- `nix build .#container` completes without chmod errors
- `nix develop` spawns a nested container
- Nested container logs "Nested container detected"
