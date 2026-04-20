{
  mkLaunch,
  pkgs,
  ...
}:
let
  fakeTools = pkgs.symlinkJoin {
    name = "virtie-fake-tools";
    paths = [
      (pkgs.writeShellScriptBin "qemu-system-x86_64" ''
        set -euo pipefail

        mkdir -p "$PWD/state"

        for arg in "$@"; do
          case "$arg" in
            *guest-cid=*)
              cid="''${arg##*guest-cid=}"
              printf '%s\n' "$cid" > "$PWD/state/qemu-vsock-cid"
              ;;
          esac
        done

        touch "$PWD/state/qemu-started"

        cleanup() {
          trap - EXIT INT TERM
          touch "$PWD/state/qemu-stopped"
          exit 0
        }
        trap cleanup EXIT INT TERM

        while true; do
          sleep 1
        done
      '')
      (pkgs.writeShellScriptBin "virtiofsd-workspace" ''
        set -euo pipefail

        mkdir -p "$PWD/state"
        touch "$PWD/state/virtiofsd-started"
        : > "$PWD/virtiofs.sock"

        cleanup() {
          trap - EXIT INT TERM
          rm -f "$PWD/virtiofs.sock"
          touch "$PWD/state/virtiofsd-stopped"
          exit 0
        }
        trap cleanup EXIT INT TERM

        while true; do
          sleep 1
        done
      '')
      (pkgs.writeShellScriptBin "mkfs.ext4" ''
        set -euo pipefail

        mkdir -p "$PWD/state"
        printf '%s\n' "$@" > "$PWD/state/mkfs-ext4-args"
      '')
    ];
  };

  fakeSSH = pkgs.writeShellScript "virtie-fake-ssh" ''
    set -euo pipefail

    mkdir -p "$PWD/state"
    attempt_file="$PWD/state/ssh-probe-attempt"
    destination_file="$PWD/state/ssh-destination"
    remote_command=()
    destination_seen=0
    option_takes_value=0

    for arg in "$@"; do
      if [ "$destination_seen" -eq 1 ]; then
        remote_command+=("$arg")
        continue
      fi

      if [ "$option_takes_value" -eq 1 ]; then
        option_takes_value=0
        continue
      fi

      case "$arg" in
        -o|-i|-p|-F|-J|-S|-b|-c|-D|-E|-I|-L|-l|-m|-O|-Q|-R|-W|-w)
          option_takes_value=1
          ;;
        -[46AaCfGgKkMNnqsTtVvXxYy])
          ;;
        --)
          destination_seen=1
          ;;
        -*)
          ;;
        *)
          printf '%s\n' "$arg" > "$destination_file"
          destination_seen=1
          ;;
      esac
    done

    if [ "''${#remote_command[@]}" -eq 1 ] && [ "''${remote_command[0]}" = "true" ]; then
      attempts=0
      if [ -f "$attempt_file" ]; then
        attempts=$(cat "$attempt_file")
      fi
      attempts=$((attempts + 1))
      printf '%s\n' "$attempts" > "$attempt_file"
      if [ "$attempts" -lt 3 ]; then
        exit 255
      fi
      exit 0
    fi

    exec "''${remote_command[@]}"
  '';

  manifest = pkgs.writeText "virtie-fake-manifest.json" (
    builtins.toJSON {
      identity.hostName = "virtie-fake";
      paths = {
        workingDir = ".";
        lockPath = "virtie.lock";
      };
      persistence.directories = [ "state" ];
      ssh = {
        argv = [ fakeSSH ];
        user = "agent";
      };
      qemu.argvTemplate = [
        "${fakeTools}/bin/qemu-system-x86_64"
        "-device"
        "vhost-vsock-pci,guest-cid={{VSOCK_CID}}"
      ];
      volumes = [
        {
          imagePath = "overlay.img";
          sizeMiB = 64;
          fsType = "ext4";
          autoCreate = true;
          label = null;
          mkfsExtraArgs = [ ];
        }
      ];
      virtiofs.daemons = [
        {
          tag = "workspace";
          socketPath = "virtiofs.sock";
          command = {
            path = "${fakeTools}/bin/virtiofsd-workspace";
            args = [ ];
          };
        }
      ];
    }
  );

  launchScript = mkLaunch {
    config = {
      agentspace.sandbox.launch = {
        commonInit = ''
          cd "$REPO_DIR"
        '';
        virtieManifest = manifest;
      };
    };
  };
in
{
  virtie-launch-e2e = pkgs.runCommand "virtie-launch-e2e" { } ''
    set -euo pipefail

    tmpdir="$(mktemp -d)"
    workspace_dir="$tmpdir/workspace"
    launch_log="$tmpdir/virtie.log"

    cleanup() {
      rm -rf "$tmpdir"
    }
    trap cleanup EXIT INT TERM

    mkdir -p "$workspace_dir"
    cd "$workspace_dir"

    export PATH=${fakeTools}/bin:$PATH

    if ! ${launchScript} sh -c 'test -f state/virtiofsd-started; test -f state/qemu-started; test -f virtiofs.sock; test -f overlay.img; echo AGENTSPACE_VIRTIE_OK' >"$launch_log" 2>&1; then
      echo "virtie-launch-e2e: launch script exited non-zero" >&2
      cat "$launch_log" >&2
      exit 1
    fi

    grep -F 'AGENTSPACE_VIRTIE_OK' "$launch_log" >/dev/null
    grep -F 'virtie: starting virtiofsd [workspace]' "$launch_log" >/dev/null
    grep -F 'virtie: starting qemu' "$launch_log" >/dev/null
    grep -F 'virtie: allocated vsock cid 3' "$launch_log" >/dev/null
    grep -Fx '3' "$workspace_dir/state/qemu-vsock-cid" >/dev/null
    grep -Fx 'agent@vsock/3' "$workspace_dir/state/ssh-destination" >/dev/null
    grep -Fx 'overlay.img' "$workspace_dir/state/mkfs-ext4-args" >/dev/null
    test -f "$workspace_dir/state/qemu-stopped"
    test -f "$workspace_dir/state/virtiofsd-stopped"

    mkdir -p "$out"
    cp "$launch_log" "$out/virtie.log"
  '';
}
