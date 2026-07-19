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
- [x] Complete clean-context review and pre-merge hardening.

## Appendix

The prototype pins go-fuse v2.10.1. Its `virtiofs` package is sufficient to boot the current x86_64 QEMU guest, but its vhost-user layer is young: it exposes no graceful shutdown/error API, hardcodes one request queue plus hiprio, and has limited upstream unit coverage. The packaged lifecycle patches include focused regression tests. The prototype therefore remains opt-in.

The end-to-end guest printed `STORE-FS-QMP-SHUTDOWN-OK` after reading Nix-store file and directory data and rejecting a write. The initial 500 ms QMP timeout was traced to go-fuse clearing `O_NONBLOCK` on a kick eventfd received through `SCM_RIGHTS`, which changed the same open file description retained by QEMU. QEMU then blocked draining its eventfd during device teardown before it could reply to `quit`. The packaged patch preserves nonblocking status and polls for kicks; the complete guest workload and QMP shutdown now exit successfully without increasing virtie's deadline.
