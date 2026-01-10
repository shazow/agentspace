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

## If Tests Fail

Check these recent changes in flake.nix:
- **Lines ~139-150**: Podman storage.conf and containers.conf
- **Lines ~185-187**: /etc/subuid and /etc/subgid setup
- **Lines ~192-194**: /run/.containerenv marker
- **Lines ~257-265**: Capability grants (CAP_SYS_ADMIN, CAP_FOWNER, etc.)

## Success Criteria

- `nix build .#container` completes without chmod errors
- `nix develop` spawns a nested container
- Nested container logs "Nested container detected"
