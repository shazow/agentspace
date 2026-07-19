# agentspace-storefs

`agentspace-storefs` is an experimental, read-only virtio-fs backend for sharing the host Nix store with an Agentspace QEMU guest. It uses go-fuse for the FUSE protocol, inode lifecycle, loopback filesystem operations, and vhost-user transport.

## Enable it

The upstream Rust `virtiofsd` remains the default. Opt in for only the managed `ro-store` share:

```nix
agentspace.lib.mkSandbox {
  nixStoreBackend = "go-fuse";
}
```

Writable workspace and additional virtio-fs shares continue to use `agentspace.sandbox.virtiofsd.package`.

The daemon is also exported as a flake package:

```console
nix run .#agentspace-storefs -- \
  --socket-path=/tmp/storefs.sock \
  --shared-dir=/nix/store
```

QEMU must connect that socket to a `vhost-user-fs-pci` device with shared guest memory, as it already does through the Agentspace manifest.

## Read-only policy

Every node is a go-fuse loopback node wrapped by an Agentspace policy that:

- accepts only `O_RDONLY` opens;
- strips `O_NOATIME`, which an unprivileged daemon cannot use for root-owned Nix store files;
- rejects create/truncate/append flags;
- rejects every file ioctl with `ENOTTY` rather than forwarding guest-controlled commands to the host;
- returns `EROFS` for setattr, write, allocation, copy-file-range, xattr mutation, create, link, symlink, rename, unlink, rmdir, mkdir, and mknod operations.

Guest mounts are independently marked read-only, and the host `/nix/store` permissions provide another layer of protection.

## Pinned go-fuse fixes

The Nix package pins go-fuse v2.10.1 and applies narrow source substitutions in the vendored dependency:

1. honor the caller's debug setting instead of logging every vhost-user control message;
2. wake a queue reader when QEMU disables its vring;
3. avoid a dispatch-lock deadlock after that wakeup;
4. implement the `GET_VRING_BASE` disconnect operation expected by QEMU.

These changes belong upstream if the prototype continues. They are intentionally visible in `package.nix` rather than hidden in generated vendor source.

## Prototype limitations

This is not yet a hardened security boundary:

- go-fuse's vhost-user package handles malformed guest-controlled memory with relatively young code; the Agentspace package adds focused lifecycle regression tests, but upstream coverage remains limited;
- the public `virtiofs.ServeFS` API cannot return startup/runtime errors or shut down through a context;
- transport configuration is hardcoded to one request queue plus the hiprio queue;
- `FUSE_SYNCFS` is currently answered as unimplemented; current guest boot tolerates that;
- no performance or resource-exhaustion benchmark has been completed.

The package carries a shutdown patch for go-fuse v2.10.1. The backend previously
cleared `O_NONBLOCK` on kick eventfds received with `SCM_RIGHTS`; because file
status flags are shared across duplicated descriptors, this also made QEMU's
copy blocking. QEMU then hung while draining that eventfd before replying to
QMP `quit`. The patched backend preserves `O_NONBLOCK` and waits with `poll(2)`,
and the full guest boot/read/write-rejection/shutdown smoke test now exits
successfully within virtie's existing 500 ms QMP deadline.

Keep the option experimental and retain upstream `virtiofsd` as the fallback.

## Validation

From this directory:

```console
go test -race ./...
```

From the repository root:

```console
nix build .#agentspace-storefs --no-link
nix build .#checks.x86_64-linux.virtie-manifest-go-store-contract --no-link
```

The end-to-end prototype test boots an Agentspace VM, reads an executable and directory data through `/nix/.ro-store`, checks that creating a file fails, reaches SSH, shuts QEMU down within the existing QMP deadline, and prints `STORE-FS-QMP-SHUTDOWN-OK`.
