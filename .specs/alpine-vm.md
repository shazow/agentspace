# Alpine VM Image Prototype

Experimental Alpine guest image support for the `virtie` sandbox launcher.

**Status**: In-Progress

## Goals

- Add `agentspace.sandbox.alpine = true` without changing `mkSandbox` or `mkLaunch` call signatures.
- Keep the default NixOS microvm manifest and launch behavior unchanged.
- Build an Alpine root disk from an `apko` minirootfs and launch a mutable copy from `persistence.basedir`.
- Preserve host launch settings that apply outside the guest OS, including SSH argv, workspace sharing, QMP, QGA, vsock, networking, balloon, and notifications.

Out of scope:

- Making NixOS guest modules, Home Manager modules, or Nix store overlays apply inside Alpine.
- Treating Alpine as stable or supported beyond this prototype.

Acceptance criteria:

- [x] `agentspace.sandbox.alpine` is available and defaults to `false`.
- [x] Alpine mode emits a standalone-style `virtie` manifest with Alpine kernel/initrd/root disk inputs.
- [x] Alpine mode does not emit the Nix store virtiofs share.
- [x] Alpine mode still emits workspace virtiofs when `mountWorkspace = true`.
- [x] The launch wrapper installs a mutable Alpine root disk under `persistence.basedir` on first launch.
- [x] Repo checks cover the Alpine manifest contract.
- [ ] KVM smoke test boots Alpine and attaches SSH over vsock.

## Progress

- [x] Added `alpine.nix` for Alpine image and manifest construction.
- [x] Added an `apko` minirootfs derivation pinned to Alpine v3.22 repositories.
- [x] Added an ext4 root disk derivation with Alpine user, SSH keys, OpenRC services, QGA, sudo, and agentspace compatibility paths.
- [x] Added `packages.x86_64-linux.alpine-root-disk` for targeted image builds.
- [x] Added Alpine manifest contract assertions to `checks/virtie-manifest.nix`.
- [x] Verified `nix build .#alpine-root-disk --no-link`.
- [x] Verified `nix flake check`.
- [ ] Fix Alpine boot: current KVM smoke reaches QEMU but the initramfs fails to mount the virtio root device before SSH can start.

## Appendix

- Current smoke-test failure: QEMU starts and QMP becomes ready, but the guest initramfs reports `mounting /dev/vda on /newroot failed: No such file or directory`, then panics before OpenRC starts.
- The host used for the smoke test already had vsock CID 3 in use, so the test held the CID 3 lock and used CID 4.
