# WIP: sandbox-compatible virtie hotplug VM check

## Current status

The sandboxed real VM hotplug check now passes:

```sh
nix build .#legacyPackages.x86_64-linux.realVMChecks.virtie-hotplug-real-vm --print-build-logs
```

The check launches QEMU from a normal Nix build sandbox, hotplugs the `cache`
virtiofs mount with `virtie hotplug`, verifies guest read/write through the
mount, detaches the device, and verifies state cleanup.

## Key fixes

- Boot the check VM from a generated store disk with
  `agentspace.sandbox.persistence.storeDisk = true`.
  Exporting the daemon sandbox's `/nix/store` through virtiofs failed because
  the host `/nix/store` is itself overlayfs-backed in this environment.
- Keep `vsock.enable = false` for this check because the Nix sandbox does not
  expose `/dev/vhost-vsock`.
- Use Q35 for the hotplug real-VM check. The microvm PCIe host bridge does not
  provide I/O space for hotplug root-port bridge windows, which prevents the
  guest from enumerating the hotplugged `vhost-user-fs-pci` device.
- Remove unconditional `--posix-acl --xattr` from the managed virtiofsd wrapper.
  With the Nix build sandbox source directory, forcing those flags caused the
  mounted virtiofs directory to return `Operation not supported` on guest
  access.
- Add guest-side retry/rescan around virtiofs mount after QMP device add.
- Give guest-agent commands their own longer timeout instead of reusing the
  short QMP command timeout.

## Validation

Passed:

```sh
go test ./internal/manifest
go test ./internal/hotplug ./internal/manager -run 'TestVirtioFSAttachSuccessWritesState|TestBuildQEMUCommandAddsPCIEHotplugPorts'
nix build .#legacyPackages.x86_64-linux.realVMChecks.virtie-hotplug-real-vm --print-build-logs
git diff --check
```

Notes:

- `nix fmt -- checks/virtie-hotplug-vm.nix` currently reindents the embedded
  Python heredoc in a way that breaks the shell script. Do not re-run formatter
  on that file without adjusting the heredoc structure.
- A full `go test ./...` from `virtie/` was attempted earlier but hung in
  manager lifecycle tests and was stopped. The focused tests above cover the
  touched hotplug, manager, and manifest behavior.
