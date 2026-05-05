# Tiny Sandbox Experiment

`lib.mkTinySandbox` is an experimental constructor for a Nix-built, initrd-only
appliance VM. It is separate from `lib.mkSandbox` and does not aim to provide a
full NixOS agentspace.

The first success target is intentionally narrow:

- boot a Linux kernel with a Nix-built initrd
- start OpenSSH inside the initrd
- listen on guest vsock port 22
- attach through the existing `virtie launch --ssh` flow as `agent@vsock/<cid>`

The tiny profile avoids persistent and shared storage:

- no virtiofs shares or virtiofsd daemons
- no block devices or microvm volumes
- `microvm.guest.enable = false`
- `microvm.storeOnDisk = false`
- `microvm.writableStoreOverlay = null`
- no QEMU guest agent

This means `writeFiles`, workspace mounts, Nix store sharing, and the normal
Home Manager/NixOS user environment are not part of the tiny appliance contract.
The initrd creates only the minimal passwd/group/home/ssh state needed for SSH.

OpenSSH is used for the first experiment because it matches the existing host
SSH flow. Dropbear is the expected future size reduction path, but that would
change the guest daemon implementation and should be evaluated separately.

macOS support is not a goal for this mode yet. The path depends on Linux-first
vsock and initrd appliance behavior.
