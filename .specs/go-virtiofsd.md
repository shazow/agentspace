# Go Nix-store virtiofs backend

Prototype a narrowly scoped virtio-fs backend for the read-only host Nix store using go-fuse.

**Status**: Completed

## Goals

- Serve `/nix/store` to the existing `ro-store` QEMU virtio-fs device.
- Reuse go-fuse for vhost-user transport, FUSE protocol handling, inode lifecycle, and loopback reads.
- Enforce a read-only API contract independently of host permissions.
- Keep upstream `virtiofsd` as the default and for writable workspace shares.
- Package and test the prototype reproducibly through the Agentspace flake.

Out of scope:

- Writable workspace shares.
- DAX, migration, POSIX locks, UID/GID remapping, or general-purpose passthrough.
- Declaring the go-fuse vhost-user implementation production-hardened.
- Changing the default backend during the prototype.

Acceptance criteria:

- [x] Unit tests prove writable opens and every exposed mutating operation return `EROFS`.
- [x] Unit tests prove read opens, lookup, metadata, directory traversal, symlinks, and xattr reads work.
- [x] The daemon is available as a flake package.
- [x] `agentspace.sandbox.nixStoreBackend = "go-fuse"` selects it only for `ro-store`.
- [x] Writable workspace shares continue to use upstream `virtiofsd`.
- [x] A QEMU guest boots, reaches SSH, and reads representative Nix-store content through the Go backend.
- [x] Known go-fuse transport/security limitations are documented.

## Progress

- [x] Research upstream virtiofsd and trace the Agentspace Nix-store workload.
- [x] Prove a small go-fuse adapter can boot an Agentspace guest.
- [x] Implement the tested read-only filesystem policy.
- [x] Add daemon CLI and Nix packaging.
- [x] Add opt-in manifest integration.
- [x] Run end-to-end and regression checks.
- [x] Complete clean-context review.

## Appendix

The prototype pins go-fuse v2.10.1. Its `virtiofs` package is sufficient to boot the current x86_64 QEMU guest, but its vhost-user layer is young: it exposes no graceful shutdown/error API, hardcodes one request queue plus hiprio, and has no direct unit tests. The prototype therefore remains opt-in.

The end-to-end guest printed `STORE-FS-E2E-OK` after reading Nix-store file and directory data and rejecting a write. QEMU's command completed, but virtie's 500 ms QMP quit deadline still expired while the go-fuse connection was being torn down; this remains a documented prototype limitation.
