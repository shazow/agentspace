# Virtie Specification

## Purpose

`virtie` is the host-side process manager for the agentspace sandbox launch path.

It currently owns:

- allocating a runtime vsock CID
- creating any missing auto-created volume images
- starting one `virtiofsd` process per configured share
- launching `qemu-system-*` directly from a Nix-generated argv template
- connecting to the guest over SSH as part of launch
- tearing the whole session down cleanly on exit

`microvm.nix` still evaluates the guest and builds the image we boot, but `virtie` no longer launches the `microvm-run` helper for the supported path.

## Scope

The supported v1 path is intentionally narrow:

- `virtiofs` shares only
- SSH connection only
- QEMU hypervisor only
- launch in the foreground
- one `virtie` process per sandbox session
- no reconnect command

Anything outside that path is explicitly unsupported for now.

## Non-Goals

- console support
- `9p` support
- airlock setup or cleanup
- reconnect support
- systemd-machined registration
- bridge, tap, or macvtap networking
- PCI passthrough or graphics
- generic `microvm.extraArgsScript`
- full `microvm-run` parity

## User-Facing Command

### `virtie launch <manifest> [-- <remote-cmd...>]`

Behavior:

1. load and validate the manifest
2. acquire a per-sandbox lock
3. allocate and lock a free vsock CID
4. create required host directories
5. create any missing auto-created volume images
6. start the configured `virtiofsd` daemons
7. wait for the expected virtiofs socket paths to exist
8. substitute the runtime CID into the QEMU argv template
9. launch QEMU directly
10. retry SSH until the guest is ready
11. attach the SSH session to the current terminal
12. on exit or signal, stop SSH first, then QEMU, then the `virtiofsd` daemons

## Manifest Contract

Nix generates a JSON manifest consumed by `virtie`.

Fields required for the supported workflow:

- `identity.hostName`
- `paths.workingDir`
- `paths.lockPath`
- `persistence.directories`
- `ssh.argv`
- `ssh.user`
- `qemu.argvTemplate`
- `volumes[]`
  - `imagePath`
  - `sizeMiB` when `autoCreate = true`
  - `fsType`
  - `autoCreate`
  - optional `label`
  - `mkfsExtraArgs`
- `virtiofs.daemons[]`
  - `socketPath`
  - daemon `command`
- optional `vsock.cidRange`

Rules:

- `qemu.argvTemplate` must include the literal placeholder `{{VSOCK_CID}}`
- the manifest must not embed `user@vsock/<cid>` ahead of time
- if `vsock.cidRange` is omitted, `virtie` allocates from `3..65535`

## Runtime Dependencies

`virtie` assumes:

- Nix has already produced a valid manifest and guest image inputs
- the host can access the configured socket and image paths
- `ssh` is available
- the required `mkfs.<fsType>` tools exist for any auto-created volumes
- the guest SSH service is reachable over the runtime-selected vsock CID

`virtie` does not assume:

- `systemd --user` is available
- `journalctl` is available
- `microvm-run` is present or used

## Process Model

The supported process flow is:

1. preflight and lock acquisition
2. allocate and lock a free vsock CID
3. create missing volume images
4. start the configured `virtiofsd` daemons
5. wait for the expected virtiofs sockets
6. start QEMU directly from the manifest template
7. retry SSH until the guest is ready
8. attach the SSH session
9. on exit, stop SSH, then QEMU, then the `virtiofsd` daemons

## Logging And Errors

`virtie` should:

- forward child stdout/stderr directly to the terminal
- identify which stage failed: preflight, virtiofs startup, VM startup, SSH readiness, active session, teardown
- return the foreground SSH exit status when possible

## State And Locking

`virtie` keeps:

- a per-sandbox lock file to prevent duplicate launches
- a per-CID lock file to keep concurrent sessions from choosing the same vsock CID

There is still no reconnect state in v1.

## Testing Requirements

`virtie` needs:

- unit tests for manifest validation
- unit tests for QEMU CID substitution
- unit tests for volume auto-create behavior
- unit tests for launch sequence and teardown ordering
- unit tests for SSH retry behavior
- integration coverage for the generated launch wrapper on the `virtiofs + ssh + qemu` path
