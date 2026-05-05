{
  mkLaunch,
  mkTinySandbox,
  pkgs,
  ...
}:
let
  tinyPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDb0Rki6+vyVxLBS28GTL2XfE6UCbpus0duCx8l2vjjF agentspace-tiny-e2e";
  tinyPrivateKey = pkgs.writeText "agentspace-tiny-e2e-id_ed25519" ''
    -----BEGIN OPENSSH PRIVATE KEY-----
    b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
    QyNTUxOQAAACA29EZIuvr8lcSwUtvBky9l3xOlAm6brNHbgsfJdr44xQAAAJjTv2Ip079i
    KQAAAAtzc2gtZWQyNTUxOQAAACA29EZIuvr8lcSwUtvBky9l3xOlAm6brNHbgsfJdr44xQ
    AAAEB/Hj0k59Nf3i7ZlAmbbqJilR0evigNBxRiHYny4TgLWTb0Rki6+vyVxLBS28GTL2Xf
    E6UCbpus0duCx8l2vjjFAAAAE2FnZW50c3BhY2UtdGlueS1lMmUBAg==
    -----END OPENSSH PRIVATE KEY-----
  '';

  vmTinyE2E = mkTinySandbox {
    hostName = "agent-tiny-e2e";
    machine = {
      memory = 192;
      vcpu = 1;
    };
    ssh.authorizedKeys = [ tinyPublicKey ];
    ssh.identityFile = "id_ed25519";
    persistence.basedir = ".agentspace-tiny-e2e";
    writeFiles."/tmp/tiny-e2e-write-file" = {
      content = "dGlueS1lMmUtd3JpdGUtZmlsZXMtb2s=";
      mode = "0644";
    };
    extraModules = [
      {
        microvm.cpu = "max";
        fileSystems."/".options = pkgs.lib.mkForce [
          "mode=0755"
          "size=32M"
        ];
      }
    ];
  };

  launchScript = mkLaunch vmTinyE2E;
  manifest = vmTinyE2E.config.agentspace.tinySandbox.launch.virtieManifestData;

  tinyConfig =
    assert manifest.qemu.memory.sizeMiB == 192;
    assert manifest.qemu.smp.cpus == 1;
    assert manifest.qemu.cpu.enableKvm == false;
    assert manifest.qemu.guestAgent.socketPath == "qga.sock";
    assert builtins.length manifest.qemu.devices.virtiofs == 1;
    assert manifest.qemu.devices.block == [ ];
    assert manifest.volumes == [ ];
    assert builtins.length manifest.virtiofs.daemons == 1;
    assert manifest.writeFiles."/tmp/tiny-e2e-write-file".content == "dGlueS1lMmUtd3JpdGUtZmlsZXMtb2s=";
    true;
in
{
  tiny-sandbox-e2e =
    assert tinyConfig;
    pkgs.runCommand "tiny-sandbox-e2e" { } ''
        set -euo pipefail

        tmpdir="$(mktemp -d)"
        launch_log="$tmpdir/tiny-sandbox-e2e.log"
        workspace_dir="$tmpdir/workspace"
        mkdir -p "$workspace_dir"

        if [ ! -e /dev/vhost-vsock ]; then
          echo "tiny-sandbox-e2e: skipped because /dev/vhost-vsock is not visible" >&2
          echo "Run with host device access, for example: nix build --option sandbox false .#checks.x86_64-linux.tiny-sandbox-e2e" >&2
          touch "$out"
          exit 0
        fi

        cleanup() {
          status=$?
          if [ "$status" -ne 0 ]; then
            echo "tiny-sandbox-e2e: failed with status $status" >&2
            if [ -f "$launch_log" ]; then
              echo "== $launch_log ==" >&2
              cat "$launch_log" >&2
            fi
          fi
          if [ -n "''${lock_pid:-}" ]; then
            kill "$lock_pid" 2>/dev/null || true
            wait "$lock_pid" 2>/dev/null || true
          fi
          rm -rf "$tmpdir"
        }
        trap cleanup EXIT INT TERM

        install -m 0600 ${tinyPrivateKey} "$workspace_dir/id_ed25519"

        mkdir -p /tmp/agentspace-vsock
        (
          ${pkgs.util-linux}/bin/flock -n 9
          sleep 90
        ) 9>/tmp/agentspace-vsock/3.lock &
        lock_pid=$!

        export XDG_RUNTIME_DIR="$tmpdir/run"
        mkdir -p "$XDG_RUNTIME_DIR"

        cd "$workspace_dir"
        timeout 70s ${launchScript} sh -c 'test "$(cat /tmp/tiny-e2e-write-file)" = tiny-e2e-write-files-ok; printf tiny-sandbox-e2e-ok' >"$launch_log" 2>&1

        grep -F 'tiny-sandbox-e2e-ok' "$launch_log" >/dev/null
        grep -F 'allocated vsock cid' "$launch_log" >/dev/null
        grep -F 'stats:' "$launch_log" >/dev/null
        test -f "$workspace_dir/.agentspace-tiny-e2e/virtie-agent-tiny-e2e.json"
        grep -F '"guestAgent":{"socketPath":"qga.sock"}' "$workspace_dir/.agentspace-tiny-e2e/virtie-agent-tiny-e2e.json" >/dev/null
        grep -F '"virtiofs":[{"id":"fs0"' "$workspace_dir/.agentspace-tiny-e2e/virtie-agent-tiny-e2e.json" >/dev/null
        grep -F '"tag":"workspace"' "$workspace_dir/.agentspace-tiny-e2e/virtie-agent-tiny-e2e.json" >/dev/null
        grep -F '"/tmp/tiny-e2e-write-file"' "$workspace_dir/.agentspace-tiny-e2e/virtie-agent-tiny-e2e.json" >/dev/null
        grep -F '"block":[]' "$workspace_dir/.agentspace-tiny-e2e/virtie-agent-tiny-e2e.json" >/dev/null

        touch "$out"
      '';
}
