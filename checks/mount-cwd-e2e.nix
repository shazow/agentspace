{
  mkLaunch,
  mkSandbox,
  pkgs,
  ...
}:
let
  vm = mkSandbox {
    machine = {
      memory = 512;
      vcpu = 1;
    };
    persistence = {
      homeImage = null;
      storeOverlay = "overlay.img";
      storeOverlaySize = 256;
      storeDisk = true;
    };
    workspace = {
      enable = true;
      addCurrentDir = true;
    };
    extraModules = [
      (
        { lib, pkgs, ... }:
        {
          # virtiofsd's default namespace sandbox needs to create namespaces
          # and pivot_root, which the unprivileged Nix build sandbox denies,
          # so the daemon exits immediately and virtle fails with "virtiofs
          # startup: ... exited with code 1". The build sandbox already
          # isolates this test; run virtiofsd without its own sandbox.
          agentspace.sandbox.virtiofsd.extraArgs = [
            "--sandbox"
            "none"
          ];
          environment.systemPackages = lib.mkForce [
            pkgs.bashInteractive
            pkgs.coreutils
            pkgs.systemd
            pkgs.util-linux
          ];
          microvm.shares = lib.mkForce [
            {
              proto = "virtiofs";
              tag = "workspace_cwd";
              source = ".";
              mountPoint = "/mnt/cwd";
              securityModel = "mapped";
            }
          ];
        }
      )
    ];
  };
  launchScript = mkLaunch vm;
in
{
  mount-cwd-real-boot =
    pkgs.runCommand "mount-cwd-real-boot"
      {
        nativeBuildInputs = [ pkgs.coreutils ];
        requiredSystemFeatures = [ "kvm" ];
      }
      ''
        set -euo pipefail

        if [ ! -r /dev/kvm ] || [ ! -w /dev/kvm ]; then
          echo "mount-cwd-real-boot: readable and writable /dev/kvm is required" >&2
          exit 1
        fi

        workspace_root="''${WORKSPACE:-}"
        if [ -z "$workspace_root" ]; then
          workspace_root="''${TMPDIR:-$PWD}"
        fi
        test_root=$(mktemp -d "$workspace_root/.mc.XXXXXX")
        project_dir="$test_root/p"

        export HOME="$test_root/home"
        export XDG_RUNTIME_DIR="$test_root/runtime"

        cleanup() {
          status=$?
          rm -r -- "$test_root"
          exit "$status"
        }
        trap cleanup EXIT

        mkdir -p "$HOME" "$XDG_RUNTIME_DIR" "$project_dir"
        chmod 700 "$XDG_RUNTIME_DIR"
        printf '%s\n' mounted > "$project_dir/sentinel"
        cd "$project_dir"

        timeout --kill-after=15s 90s ${launchScript} \
          /bin/sh -c 'test -f /home/agent/workspace/p/sentinel'

        echo "mount-cwd-real-boot: passed"
        touch "$out"
      '';
}
