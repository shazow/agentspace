{
  mkLaunch,
  pkgs,
  ...
}:
let
  fakeRunner = pkgs.symlinkJoin {
    name = "virtie-fake-runner";
    paths = [
      (pkgs.writeShellScriptBin "microvm-run" ''
        set -euo pipefail

        mkdir -p "$PWD/state"
        touch "$PWD/state/microvm-started"

        cleanup() {
          touch "$PWD/state/microvm-stopped"
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
          rm -f "$PWD/virtiofs.sock"
          touch "$PWD/state/virtiofsd-stopped"
        }
        trap cleanup EXIT INT TERM

        while true; do
          sleep 1
        done
      '')
    ];
  };

  fakeSSH = pkgs.writeShellScript "virtie-fake-ssh" ''
    set -euo pipefail

    mkdir -p "$PWD/state"
    attempt_file="$PWD/state/ssh-probe-attempt"
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
        microvmRun = "${fakeRunner}/bin/microvm-run";
        lockPath = "virtie.lock";
      };
      persistence.directories = [ "state" ];
      ssh.argv = [ fakeSSH "agent@example" ];
      virtiofs.daemons = [
        {
          tag = "workspace";
          socketPath = "virtiofs.sock";
          command = {
            path = "${fakeRunner}/bin/virtiofsd-workspace";
            args = [ ];
          };
        }
      ];
    }
  );

  launchScript = mkLaunch {
    config = {
      microvm.declaredRunner.outPath = fakeRunner;
      agentspace.sandbox.launch = {
        commonInit = ''
          cd "$REPO_DIR"
        '';
        sshArgv = [ ];
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

    if ! ${launchScript} sh -c 'test -f state/virtiofsd-started; test -f state/microvm-started; test -f virtiofs.sock; echo AGENTSPACE_VIRTIE_OK' >"$launch_log" 2>&1; then
      echo "virtie-launch-e2e: launch script exited non-zero" >&2
      cat "$launch_log" >&2
      exit 1
    fi

    grep -F 'AGENTSPACE_VIRTIE_OK' "$launch_log" >/dev/null
    grep -F 'virtie: starting virtiofsd [workspace]' "$launch_log" >/dev/null
    grep -F 'virtie: starting microvm-run' "$launch_log" >/dev/null
    test -f "$workspace_dir/state/microvm-stopped"
    test -f "$workspace_dir/state/virtiofsd-stopped"

    mkdir -p "$out"
    cp "$launch_log" "$out/virtie.log"
  '';
}
