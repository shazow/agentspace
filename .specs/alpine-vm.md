# Alpine VM Image Prototype

Experimental Alpine guest image support for the `virtie` sandbox launcher.

**Status**: Concluded

## Goals

- Add `agentspace.sandbox.alpine.enable = true` without changing `mkSandbox` or `mkLaunch` call signatures.
- Expose `agentspace.lib.mkAlpineRootDisk` so consumers can choose a custom Alpine root disk builder without editing agentspace flake code.
- Keep the default NixOS microvm manifest and launch behavior unchanged.
- Build an Alpine root disk from an `apko` minirootfs and launch a mutable copy from `persistence.basedir`.
- Preserve host launch settings that apply outside the guest OS, including SSH argv, workspace sharing, QMP, QGA, vsock, networking, balloon, and notifications.

Out of scope:

- Making NixOS guest modules, Home Manager modules, or Nix store overlays apply inside Alpine.
- Treating Alpine as stable or supported beyond this prototype.

Acceptance criteria:

- [x] `agentspace.sandbox.alpine.enable` is available and defaults to `false`.
- [x] `agentspace.sandbox.alpine.rootDiskBuilder` defaults to the built-in Alpine root disk builder.
- [x] `agentspace.lib.mkAlpineRootDisk` is available for downstream flakes.
- [x] Alpine mode emits a standalone-style `virtie` manifest with Alpine kernel/initrd/root disk inputs.
- [x] Alpine mode does not emit the Nix store virtiofs share.
- [x] Alpine mode still emits workspace virtiofs when `mountWorkspace = true`.
- [x] The launch wrapper installs a mutable Alpine root disk under `persistence.basedir` on first launch.
- [x] Repo checks cover a custom Alpine root disk builder.
- [x] Repo checks cover the Alpine manifest contract.
- [x] KVM smoke test boots Alpine and attaches SSH over vsock.

## Progress

- [x] Added `alpine.nix` for Alpine image and manifest construction.
- [x] Added an `apko` minirootfs derivation pinned to Alpine v3.22 repositories.
- [x] Added an ext4 root disk derivation with Alpine user, SSH keys, OpenRC services, QGA, sudo, and agentspace compatibility paths.
- [x] Added `packages.x86_64-linux.alpine-root-disk` for targeted image builds.
- [x] Added Alpine manifest contract assertions to `checks/virtie-manifest.nix`.
- [x] Split Alpine root disk construction into `alpine-root-disk.nix`.
- [x] Exposed `agentspace.lib.mkAlpineRootDisk` as the experimental public root disk builder.
- [x] Changed the Alpine module API from `alpine = true` to `alpine.enable = true`.
- [x] Verified `nix build .#alpine-root-disk --no-link`.
- [x] Verified `nix flake check`.
- [x] Fixed Alpine boot by refreshing the pinned rootfs hash, loading ext4 dependencies strictly, rerunning `mdev` after initramfs module load, and providing minimal OpenRC networking.
- [x] Fixed vsock SSH by loading guest vsock modules before starting the vsock forwarder and using an unlockable shadow entry for the `agent` user.
- [x] Verified KVM smoke with a small test VM: 128 MiB RAM, 1 vCPU, `mountWorkspace = false`, CID 4. It requires at least 65 MiB in this environment; a 64 MiB smoke booted but did not complete SSH reliably.

## Conclusion

The Alpine VM path reached a useful prototype stage: it can produce a root
disk, emit a standalone-style `virtie` manifest, boot under QEMU, and accept SSH
over the expected guest path. We are stopping the experiment here for now
because the default rootfs build is not reliable enough to continue building on
as the next VM direction.

The main blocker is that the base Alpine rootfs is built from mutable upstream
package repositories while Nix treats the result as a fixed-output derivation.
`apko build-minirootfs` resolves package names against Alpine `v3.22`
`APKINDEX.tar.gz` files at build time. Alpine release repositories still receive
package updates, so the same repository URL and package list can resolve to
different APK versions later. That changes the produced `rootfs.tar` and causes
`rootfsOutputHash` to go stale even when no agentspace source file changed.

We confirmed that the local `apko` has an `apko lock` command and that the lock
file records package URLs, versions, and checksums. However, this version's
`build-minirootfs` help does not expose a `--lockfile` flag, and a probe showed
that `build-minirootfs` still contacts `APKINDEX.tar.gz` with an
`apko.lock.json` present in the workdir. The regular `apko build` command does
expose `--lockfile`, so it is the better starting point if we return to an
Alpine-like package-rootfs builder.

## Lessons for the Next VM

- Keep the package base and VM-specific mutable configuration as separate
  artifacts. The current Alpine builder mixes upstream package resolution with
  hostname, SSH keys, user setup, OpenRC services, workspace mount setup, and
  compatibility paths in the final disk construction.
- If using an APK/apko-style base again, build a locked, hash-stable base rootfs
  from committed config and lock data. Updating packages should be an explicit
  maintenance step: regenerate the lock, refresh the expected base hash, rebuild,
  and rerun checks.
- Generate host/user-specific guest configuration as a separate overlay applied
  after extracting the stable base. This overlay should contain SSH
  `authorized_keys`, `/etc/hostname`, `/etc/hosts`, service definitions,
  workspace mount configuration, user/sudo state, and agentspace compatibility
  links.
- Avoid requiring a fixed-output derivation to track a moving package
  repository. A fixed-output hash is useful only after every upstream input is
  pinned or fetched through Nix with its own hash.
- Treat custom initramfs and kernel module handling as a major complexity cost.
  The Alpine prototype needed explicit ext4, virtio block, virtio net, and vsock
  handling, plus careful device discovery and `mdev` timing.
- Keep the next VM small for testing, but budget enough memory for reliable SSH
  readiness. In this environment, 64 MiB booted but was not reliable; 128 MiB
  worked for the smoke test.
- Surface portability limits early. This prototype is Linux/QEMU focused and the
  KVM/vsock path is not obviously portable to macOS without a different launch
  strategy.

## Shortcomings

- The default Alpine root disk package can fail in a clean store when the pinned
  fixed-output rootfs hash no longer matches the mutable Alpine repository
  contents.
- Custom package sets remain awkward because changing `packages`,
  `extraPackages`, `repositories`, or `apkoArch` requires a matching rootfs hash
  update and gives consumers little guidance.
- The public `mkAlpineRootDisk` hook is useful for experimentation, but the
  builder exposes too much of the prototype's internal packaging instability.
- The image construction is tightly coupled to Alpine/OpenRC details, so it is
  not a good general foundation for the next VM implementation.
- The smoke test validates the happy path, but it is expensive and does not make
  package-resolution drift obvious until the fixed-output build fails.

## Appendix

- The host used for smoke tests already had vsock CID 3 in use, so tests held the CID 3 lock and used CID 4.
- Validation after the boot fix:
  - `nix build .#alpine-root-disk --no-link --print-out-paths`
  - `nix flake check --no-build`
  - `nix flake check`
- Rootfs hash drift observed after the initial prototype:
  - stale specified hash:
    `sha256-gelXx81rvXpb2z+5w3yltm+BQuPAsrHL3uDwAsqGVlw=`
  - later produced hash:
    `sha256-t8GHcYNX7NPd0P0zgSxJznjl5p0o1mMbhl7HEy4cR4g=`
