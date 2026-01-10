# Agent Prompt: Recursive Container Sandbox

## Original Task

Design a Nix flake which opens a secure container for coding agents, configured such that we can run it again inside the container to open more containers (recursive nesting).

## Current State

**Status**: Configuration complete, needs testing from a fresh environment.

We are currently inside one of these containers, but it was started *before* the `withPrivileges = true` change, so we cannot test nested containers from here.

## What Has Been Done

1. **Enabled `withPrivileges = true`** in the default sandbox configuration (line ~295 in flake.nix)

2. **Added required capabilities** for nested containers and Nix builds:
   - `CAP_SYS_ADMIN` - needed for user namespaces and mount operations
   - `CAP_FOWNER` - needed for Nix store chmod operations
   - `CAP_CHOWN` - needed for ownership changes
   - `CAP_SETUID` / `CAP_SETGID` - needed for user namespace mapping

3. **Added container tooling** to image when `withPrivileges = true`:
   - `podman` - container runtime
   - `slirp4netns` - rootless networking
   - `fuse-overlayfs` - rootless storage driver

4. **Updated Nix configuration** for container compatibility:
   - `use-sqlite-wal = false` - works better in containers
   - `require-sigs = false` - allows local builds without signature verification

5. **Documented** the recursive container design in README.md

## What Still Needs Testing

1. **Exit current container** and rebuild from host:
   ```bash
   nix develop
   ```

2. **Verify capabilities** inside the new container:
   ```bash
   cat /proc/self/status | grep Cap
   # Should show non-zero capability values
   ```

3. **Test Nix builds** work inside the container:
   ```bash
   nix build .#container
   ```

4. **Test nested container launch**:
   ```bash
   nix develop  # Should spawn a second-level container
   ```

5. **Verify nesting detection** - the second-level container should log:
   ```
   🛡️ Nested container detected. Using default runtime (runc).
   ```

## Key Files

- `flake.nix` - Main configuration, `mkSandbox` function defines container setup
- `README.md` - User documentation
- `PROMPT.md` - This file

## Key Code Locations

- **Nesting detection**: flake.nix lines 219-224
- **Capability grants**: flake.nix lines 241-248
- **Nix configuration**: flake.nix lines 120-128
- **Image contents**: flake.nix lines 107-136

## Known Limitations

1. **gVisor disabled when nested** - Falls back to runc for compatibility
2. **Security reduced with `withPrivileges`** - Necessary trade-off for nesting capability
3. **Nix sandbox disabled** - Required because container environments can't create nested sandboxes

## Future Improvements to Consider

- Add depth limit detection to prevent infinite nesting
- Implement capability reduction at each nesting level
- Add option to use VM-based isolation (qcow2 output exists) instead of containers for stronger boundaries
- Consider using `bubblewrap` as an alternative to nested podman for lighter isolation
