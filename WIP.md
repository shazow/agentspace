# WIP: Graphical Real Boot Smoke

We are trying to make the opt-in graphical real boot smoke test complete reliably.

Target test:

```sh
nix build .#legacyPackages.x86_64-linux.graphicalChecks.graphical-real-boot-smoke --no-link --option sandbox false
```

The smoke test boots a real `virtie` VM with `microvm.graphics.enable = true`,
then SSHes into the guest and asserts:

- `/sys/class/drm/card*` exists
- `run-wayland-proxy` exists

This is intentionally not a pure Nix build. QEMU needs host runtime access,
especially `/dev/vhost-vsock`, and the derivation has `__noChroot = true`.

## Current Uncommitted Changes

1. `agentspace.sandbox.groups`
   - Added in `sandbox-qemu.nix`.
   - Default is intentionally `[ "wheel" "kvm" ]`.
   - Wired to `users.users.${cfg.user}.extraGroups`.
   - `checks/consumer-workflow.nix` now sets/asserts `[ "wheel" "kvm" ]`
     and asserts the guest user receives `sandboxCfg.groups`.

2. Graphical smoke fixture changes in `checks/graphical.nix`
   - Uses `accel = "kvm:tcg"` instead of pure `tcg`.
   - Removes `pit = "off"`.
   - Removes the old `nomodeset=0` kernel-param append.
   - Uses a short host runtime dir:
     `XDG_RUNTIME_DIR="$(mktemp -d /tmp/virtie-graphical.XXXXXX)"`.
   - Cleans that runtime dir with a trap.
   - Sets `VIRTIE_SSH_READY_TIMEOUT=5m`.
   - Raises outer shell `timeout` from `240s` to `420s`.
   - Temporarily enables serial-console boot logging for diagnosis:
     `microvm.qemu.serialConsole = lib.mkForce true`,
     `boot.consoleLogLevel = lib.mkForce 7`,
     `boot.initrd.verbose = lib.mkForce true`,
     and forced kernel params:
     `[ "earlyprintk=ttyS0" "console=ttyS0" "udev.log_level=3" ]`.

3. `virtie` SSH readiness timeout override
   - Added `VIRTIE_SSH_READY_TIMEOUT` support in
     `virtie/internal/manager/ssh_ready.go`.
   - `newManager()` now reads that env var, falling back to the existing
     `2m` default for unset, invalid, or non-positive values.
   - Added manager tests for valid and invalid env values.

## Validation Completed

Passed:

```sh
CGO_ENABLED=0 go test ./...
nix flake check
```

Note: plain `go test ./...` failed locally because CGO tried to use `gcc`,
which is not installed in this VM. The flake builds `virtie` with
`CGO_ENABLED=0`, and the CGO-disabled test run passed.

The first attempted `nix flake check --no-link` was invalid because
`nix flake check` does not accept `--no-link`.

## Confirmed Findings

- Running the graphical derivation as `agent` still fails before VM launch:
  `agent` is not a trusted Nix user, so Nix ignores
  `--option sandbox false` and rejects the `__noChroot` derivation while
  sandboxing is still true.
- Passwordless `sudo` is available, and running the target with
  `sudo -n nix build ... --option sandbox false` does launch the derivation.
- In this VM, the current `agent` session is in `kvm`, and both
  `/dev/kvm` and `/dev/vhost-vsock` were present and mode `crw-rw-rw-`.
- Under `sudo nix build ... --option sandbox false`, the derivation starts
  QEMU, allocates CID `4`, reaches QMP readiness quickly, then waits for SSH
  readiness.
- Extending virtie's SSH readiness timeout from `2m` to `5m` works: the next
  sudo derivation ran until the `5m` readiness deadline instead of the old
  `2m` deadline.
- Even with the longer timeout, the derivation still timed out waiting for
  SSH readiness:
  `vm startup: wait for ssh readiness: context deadline exceeded`.
- The log from the 5-minute run only had host-side output, so serial-console
  logging was added to the fixture to expose guest boot progress.
- A serial-enabled retry was started but interrupted by the user before we got
  a final pass/fail log. After interruption, `pgrep` found no remaining
  `nix build`, `graphical-real-boot-smoke`, `qemu-system`, `virtie launch`,
  or `virtiofsd` processes.

## Outstanding Problems

1. The core failure is unresolved: inside the Nix derivation, the guest never
   emits the `virtie.ssh.ready` token before the readiness timeout.

2. We do not yet know whether the guest is:
   - failing before userspace,
   - booting but not mounting the writable `/nix` overlay correctly,
   - booting but failing OpenSSH or the readiness service,
   - blocked by builder-user permissions or Nix build environment differences,
   - or blocked by the graphical/KVM fixture configuration.

3. The serial-console diagnostic change is still provisional. On resume, rerun
   the sudo graphical derivation and inspect the guest boot log. If it reveals
   the root cause, either remove the verbose serial logging or keep a smaller
   diagnostic form only if it is useful for future failures.

## Next Resume Steps

1. Clear stale runtime files:

```sh
sudo rm -rf /tmp/agentspace-agent-sandbox.lock /tmp/agentspace-vsock /tmp/virtie-graphical.*
```

2. Rerun:

```sh
sudo -n nix build .#legacyPackages.x86_64-linux.graphicalChecks.graphical-real-boot-smoke --no-link --option sandbox false
```

3. If it fails, inspect:

```sh
nix log /nix/store/<graphical-real-boot-smoke.drv>
```

4. Use the serial log to determine whether to fix guest boot/mount/service
   behavior, permissions, or the fixture.

5. Repeat:

```sh
CGO_ENABLED=0 go test ./...
nix flake check
```

6. Once the smoke test passes or the remaining blocker is documented clearly,
   delete `WIP.md` before final completion, per the original task request.
