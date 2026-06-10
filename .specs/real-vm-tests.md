# Real VM Tests

Migrate selected repo checks from fake launch harnesses to fast real VM consumer workflows.

**Status**: Implemented

## Goals

Add real VM-running checks that prove the supported consumer workflow works through the generated launch wrapper, `virtie`, QEMU, guest readiness, vsock SSH, and guest-visible mounts.

- Start with one default check named `consumer-real-vm-smoke`.
- Keep the first VM small and fast: 512 MiB memory, no explicit vCPU count, no persistent home image, a short hostname, headless boot with verbose kernel output, and one short SSH command.
- Boot the guest from a generated read-only store disk instead of a live `/nix/store` virtiofs share; this works inside the normal Nix sandbox and avoids initrd closure discovery failures.
- Mount the launch directory as the base workspace and verify a sentinel at `$WORKSPACE/sentinel`.
- Prefer a temporary directory under `$WORKSPACE/tmp` when `$WORKSPACE` exists and is writable for generated persistence images, sockets, and runtime state; otherwise fall back to the normal Nix sandbox build directory.
- Preserve the existing fake `virtie-e2e` checks because they cover host-side lifecycle behavior such as suspend/resume and failure cases without booting a VM.

Out of scope:

- Suspend/resume real VM coverage.
- Guest-agent write-files coverage.
- Balloon, graphical, auth-failure, notification, and tunnel coverage.
- Workspace `mount_cwd` bind coverage until the guest-side mount helper handles existing virtiofs mount points without metadata operations that fail on mapped virtiofs.
- Adding new dependencies.

Acceptance criteria:

- [x] `checks/default.nix` includes a real VM check that boots through `mkSandbox` and `mkLaunch`.
- [x] The check verifies a sentinel file in the launch directory is visible in the guest at `$WORKSPACE/sentinel`.
- [x] The check attaches over vsock SSH and prints `AGENTSPACE_REAL_VM_OK` from the guest.
- [x] The check copies launch logs into `$out` for failure diagnosis.
- [x] The check runs in the normal Nix sandbox and fails fast with an environment message if `/dev/vhost-vsock` is not visible.

## Progress

- [x] Confirmed current default checks include fake `virtie-e2e` coverage but not a default real VM boot.
- [x] Confirmed the opt-in graphical smoke check does boot a real VM, but it is too heavy and specialized for the first default real VM check.
- [x] Confirmed the current Nix sandbox exposes `/dev/kvm` but not `/dev/vhost-vsock`.
- [x] Confirmed `__noChroot` is rejected by the current sandboxed Nix daemon and cannot be used for this check.
- [x] Confirmed `/dev/vhost-vsock` is visible in the fresh sandboxed environment.
- [x] Confirmed `/home/agent/workspace/tmp` is not writable inside the current Nix build sandbox unless the workspace is exposed, so the check uses `$WORKSPACE/tmp` only when available and writable.
- [x] Confirmed long build sandbox paths can exceed Unix socket path limits for generated `virtiofsd` sockets, so the check uses a short hostname and short scratch directory names.
- [x] Confirmed the store-disk VM boots inside the sandbox without explicit manifest, kernel, initrd, store disk, or VM system closure retention attrs.
- [x] Confirmed a live `/nix/store` virtiofs share still fails in initrd with `Failed to start Find NixOS closure` inside a sandboxed derivation.
- [x] Confirmed `workspace.addCurrentDir = true` reaches guest boot but fails in `virtie`'s guest-side `workspace cwd mount` step because `install -d /home/agent/workspace /home/agent/workspace/w` tries a metadata operation on the existing mapped virtiofs mount point.
- [x] Chose SSH plus base workspace mounting as the first real VM consumer workflow.
- [x] Add `checks/real-vm.nix`.
- [x] Import the new check from `checks/default.nix`.
- [x] Confirmed `nix eval .#checks.x86_64-linux.consumer-real-vm-smoke.drvPath` succeeds.
- [x] Confirmed the targeted build fails fast in the current environment because `/dev/vhost-vsock` is not sandbox-visible.
- [x] Confirmed `nix build .#checks.x86_64-linux.consumer-real-vm-smoke` succeeds in the fresh environment with sandbox-visible `/dev/vhost-vsock`.
- [x] Validate full `nix flake check`.
- [x] Removed debugging shims after validating the simplified store-disk check shape.

## Appendix

Proposed first check shape:

```nix
consumer-real-vm-smoke = pkgs.runCommand "consumer-real-vm-smoke" {
  nativeBuildInputs = [
    pkgs.coreutils
  ];
} ''
  set -euo pipefail

  if [ ! -e /dev/vhost-vsock ]; then
    echo "consumer-real-vm-smoke: /dev/vhost-vsock is not visible in the Nix sandbox" >&2
    echo "consumer-real-vm-smoke: add /dev/vhost-vsock to nix.settings.extra-sandbox-paths" >&2
    exit 1
  fi

  if [ -n "''${WORKSPACE:-}" ] && mkdir -p "$WORKSPACE/tmp" 2>/dev/null; then
    scratch_parent="$WORKSPACE/tmp"
  else
    scratch_parent="''${NIX_BUILD_TOP:-$PWD}/tmp"
    mkdir -p "$scratch_parent"
  fi

  base_dir="$scratch_parent/rv.$RANDOM"
  launch_dir="$base_dir/w"
  log="$base_dir/launch.log"
  mkdir -p "$launch_dir" "$base_dir/home" "$base_dir/runtime"
  chmod 700 "$base_dir/home" "$base_dir/runtime"

  export HOME="$base_dir/home"
  export XDG_RUNTIME_DIR="$base_dir/runtime"

  cd "$launch_dir"
  printf 'real vm smoke\n' > sentinel
  install -m 0600 ${sshKeys.graphical.privateKey} ./id_ed25519

  timeout 180s ${launchScript} bash -lc '
    test -f "$WORKSPACE/sentinel"
    echo AGENTSPACE_REAL_VM_OK
  ' >"$log" 2>&1

  mkdir -p "$out"
  cp "$log" "$out/launch.log"
  grep -F AGENTSPACE_REAL_VM_OK "$out/launch.log" >/dev/null
'';
```

The actual implementation should use `mktemp -d "$WORKSPACE/tmp/agentspace-real-vm-smoke.XXXXXX"` instead of `$RANDOM` if available in the builder environment, and should clean up the temporary directory on success.

Minimal sandbox configuration for the check:

```nix
realVM = mkSandbox {
  hostName = "rv";
  ssh.authorizedKeys = [ sshKeys.graphical.publicKey ];
  ssh.exec = mkExecSSH {
    identityFile = "./id_ed25519";
  };
  persistence = {
    baseDir = ".agentspace";
    homeImage = null;
    storeDisk = true;
  };
  quiet = false;
  machine.memory = 512;
  workspace = {
    enable = true;
    hostDir = ".";
    addCurrentDir = false;
  };
};
```

Do not set `machine.vcpu`; leaving it unset lets `virtie` use the host-visible CPU count.

Current environment note:

- Normal sandboxed derivations can see `/dev/kvm`.
- The fresh sandboxed environment can see `/dev/vhost-vsock`.
- The current sandboxed build cannot write to `/home/agent/workspace/tmp` unless the workspace path is also exposed by the daemon sandbox configuration.
- The first real VM check needs host vsock because the consumer SSH workflow connects to `agent@vsock/<cid>`.
- As the untrusted `agent` user, passing `--option sandbox-paths ...` is ignored. The Nix daemon environment needs to be changed instead.

Recommended environment change to make vsock runnable:

```nix
nix.settings.extra-sandbox-paths = [
  "/dev/vhost-vsock=/dev/vhost-vsock"
];
```

Optional environment change to let the check use `$WORKSPACE/tmp` rather than the build directory:

```nix
nix.settings.extra-sandbox-paths = [
  "/home/agent/workspace/tmp=/home/agent/workspace/tmp"
];
```

After changing the Nix daemon config, verify with:

```sh
nix build --no-link --impure --expr 'let pkgs = import <nixpkgs> {}; in pkgs.runCommand "sandbox-vhost-vsock-visibility" {} "set -eu; test -e /dev/vhost-vsock; ls -l /dev/vhost-vsock > $out"'
```

The check should not use `__noChroot`; the current daemon rejects it while sandboxing is enabled.

Validation commands after implementing the check:

```sh
nix build .#checks.x86_64-linux.consumer-real-vm-smoke
unlink result
nix flake check
unlink result
```
