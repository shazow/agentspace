# WIP: sandbox-compatible virtie hotplug VM check

## Goal

Make `checks/virtie-hotplug-vm.nix` run end-to-end as a normal Nix build under
the daemon sandbox, without requiring `sudo nix build --option sandbox false`.

The check should launch a real QEMU VM, attach the `cache` virtiofs hotplug mount
with `virtie hotplug`, verify guest read/write through the mount, detach it, and
verify cleanup.

## What changed so far

- Removed `__noChroot = true` from `checks/virtie-hotplug-vm.nix`.
- Added `requiredSystemFeatures = [ "kvm" ]` so the check declares its KVM
  requirement instead of bypassing sandboxing.
- Changed tempdir creation to use `${TMPDIR:-/tmp}` so it is compatible with the
  Nix build sandbox.
- Removed the SSH-based test flow and switched the check toward QGA-driven guest
  execution.
- Added sandbox options:
  - `agentspace.sandbox.ssh.readySocket`, defaulting to `ready.sock`.
  - `agentspace.sandbox.vsock.enable`, defaulting to `true`.
- Added manifest support for disabled vsock:
  - `[vsock].disabled = true`
  - validation skips vsock transport checks when disabled
  - QEMU args omit the `vhost-vsock` device when disabled
- Set the hotplug VM to `vsock.enable = false` because the Nix sandbox does not
  expose `/dev/vhost-vsock`.
- Added more verbose lifecycle logging:
  - full `virtie hotplug` attach/detach state transitions
  - manager hotplug lifecycle
  - full QEMU argv in verbose launch mode
- Changed foreground non-SSH launch to release its QMP connection after guest
  readiness so a separate `virtie hotplug` process can connect to QMP.
- Added explicit check references to:
  - `hotplugVM.config.system.build.toplevel`
  - `pkgs.closureInfo { rootPaths = [ systemClosure ]; }`
  so the VM system closure is a direct derivation input.
- Tried setting `microvm.virtiofsd.extraArgs = [ "--sandbox=none" ]` for this
  check VM.

## Validation run so far

`go test ./...` from `virtie/` passed after the logging, manifest, and QMP
changes.

`git diff --check` passed earlier in the session.

The target command has been run repeatedly as the normal user:

```sh
nix build .#legacyPackages.x86_64-linux.realVMChecks.virtie-hotplug-real-vm --print-build-logs
```

It no longer fails on:

- `__noChroot` under sandboxing
- missing `/dev/vhost-vsock`
- QMP contention between `virtie launch` and `virtie hotplug`

It still does not pass.

## Failures observed

### 1. Original sandbox blocker

Normal-user build failed because the derivation used `__noChroot = true`. Passing
`--option sandbox false` was ignored for the untrusted user. Running with `sudo`
got further, but the goal is no sudo.

### 2. Missing `/dev/vhost-vsock`

After removing `__noChroot`, QEMU startup failed under the sandbox because the
builder cannot access `/dev/vhost-vsock`.

That led to adding `vsock.enable = false` and manifest/QEMU support for disabled
vsock.

### 3. QMP ownership hang

Once vsock was disabled, `virtie hotplug` could hang because the foreground
`virtie launch` path kept an active QMP client open. QMP only accepts one client
for our socket usage.

We changed non-SSH foreground launch to log
`releasing qmp client for foreground launch`, disconnect the launch QMP client,
and reconnect later for shutdown.

Open concern: this may need a more careful suspend/shutdown design before final
merge. The existing suspend handler may still capture the original disconnected
client in this branch.

### 4. Guest boot stops in initrd emergency mode

The current failure is guest boot, not hotplug itself.

The build log shows the VM boots Linux, mounts the virtiofs `/nix/store`, and
then fails:

```text
Starting Find NixOS closure...
FAILED Failed to start Find NixOS closure.
Dependency failed for Initrd Default Target.
Started Emergency Shell.
Cannot open access to console, the root account is locked.
```

The QEMU command includes:

```text
init=/nix/store/8wvy5gnmsj966jbqd1gi51n8vjyyjdhk-nixos-system-agent-sandbox-26.05.20260524.d849bb2/init
regInfo=/nix/store/x8phf0zr8xb781zlzn01mzam5n8v1ljn-closure-info/registration
```

The check now has host-side preflight tests that these paths exist in the
builder:

```sh
test -x ${systemClosure}/prepare-root
test -f ${systemClosureInfo}/registration
```

Those tests pass, but the guest still fails `initrd-find-nixos-closure.service`.

## What did not work

- Removing `microvm.cpu = "max"` did not fix boot.
- Restoring default microvm machine options did not fix boot.
- Explicitly referencing the VM system closure and closure-info derivation in the
  check did not fix boot.
- Running the managed virtiofsd wrapper with `--sandbox=none` did not fix boot.

## Current thesis

This is likely still about how the Nix daemon build sandbox presents `/nix/store`
to the builder versus what the host-side `virtiofsd` process can present to the
guest.

The guest can mount the `ro-store` virtiofs share, so the device itself works.
But `initrd-find-nixos-closure.service` cannot resolve the `init=` NixOS system
closure from inside `/sysroot/nix/store`, even though the same path exists from
the builder's point of view.

Possible explanations:

- The sandboxed `/nix/store` visible to the builder is not a plain directory
  tree from `virtiofsd`'s perspective. It may rely on bind mounts or daemon
  namespace behavior that `virtiofsd` is not exporting as expected.
- `virtiofsd` may expose the top-level store and some entries, but not all
  sandbox input paths needed by the guest, despite explicit derivation
  references.
- The direct host preflight proves the paths exist in the build process, but not
  that they are visible through the exact `virtiofsd` process and mount namespace
  used by QEMU.
- The initrd service failure output is too terse; we need the exact missing path
  or failed command inside the initrd.

## Suggested next steps

1. Improve guest-side diagnostics for the initrd failure.
   - Add an initrd debug unit or kernel param to print:
     - `ls -ld /sysroot/nix/store/<system>`
     - `ls -l /sysroot/nix/store/<system>/prepare-root`
     - `readlink /sysroot/nix/store/<system>/init`
     - `mount`
   - The current serial log only says the service failed.

2. Confirm what `virtiofsd` sees inside the Nix build sandbox.
   - Start a small throwaway derivation or check snippet that runs the same
     virtiofsd wrapper against `/nix/store` and inspects whether the system
     closure path can be reached through the exported filesystem.
   - Avoid leaving this as a permanent test unless it becomes the final check.

3. Consider replacing the `/nix/store` virtiofs dependency for this check.
   - For a sandbox-compatible real VM check, using a microvm store disk/image
     may be more reliable than exporting the daemon sandbox's `/nix/store` with
     virtiofs.
   - This would likely mean creating a minimal store image containing the system
     closure and booting from that in the check.

4. Revisit the QMP release change before finalizing.
   - It solved the hotplug QMP contention, but the suspend/shutdown interaction
     should be reviewed.
   - A cleaner version might make shutdown/suspend reconnect to QMP on demand
     consistently, instead of retaining a disconnected client.

5. Once boot succeeds, rerun:

```sh
nix build .#legacyPackages.x86_64-linux.realVMChecks.virtie-hotplug-real-vm --print-build-logs
cd virtie && go test ./...
git diff --check
nix fmt -- checks/virtie-hotplug-vm.nix sandbox-qemu.nix
```

Clean up `./result` with `unlink result` if Nix creates it.
