{
  mkLaunch,
  mkSandbox,
  pkgs,
  virtiePackage,
  ...
}:
let
  qemuTestModule = {
    microvm.cpu = "max";
    microvm.qemu.machineOpts = {
      accel = "kvm:tcg";
      mem-merge = "on";
      acpi = "on";
      pic = "off";
      pcie = "on";
      rtc = "on";
      usb = "off";
    };
  };
  mkVm =
    {
      baseDir,
      extraModules ? [ ],
    }:
    mkSandbox {
      ssh.autoconnect = false;
      quiet = false;
      machine = {
        memory = 1024;
        vcpu = 1;
      };
      persistence = {
        inherit baseDir;
        homeImage = null;
        storeDisk = false;
        storeOverlay = "overlay.img";
      };
      workspace.enable = false;
      extraModules = [ qemuTestModule ] ++ extraModules;
    };
  persistentVm = mkVm { baseDir = "persistent-vm"; };
  nonRootVm = mkVm {
    baseDir = "non-root-vm";
    extraModules = [ ./local-overlay-store/non-root-daemon.nix ];
  };
  persistentLaunchScript = mkLaunch persistentVm;
  nonRootLaunchScript = mkLaunch nonRootVm;
in
{
  local-overlay-store-real-boot = pkgs.writeShellApplication {
    name = "local-overlay-store-real-boot";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.jq
      pkgs.gnused
    ];
    text = ''
      set -euo pipefail

      readonly guest_ready_attempts=300
      readonly guest_ready_poll_seconds=0.2
      readonly guest_command_attempts=600

      workspace_root="''${WORKSPACE:-$PWD}"
      test_root=$(mktemp -d "$workspace_root/.as-los.XXXXXX")

      export HOME="$test_root/home"
      export XDG_RUNTIME_DIR="$test_root/runtime"

      non_root_log="$test_root/non-root-boot.log"
      populate_log="$test_root/populate-upper.log"
      reboot_gc_log="$test_root/reboot-and-gc.log"
      manifest_path=
      control_socket=
      qemu_pid_file=
      qmp_socket=
      launch_pid=
      qemu_pid=

      mkdir -p "$HOME" "$XDG_RUNTIME_DIR"
      chmod 700 "$XDG_RUNTIME_DIR"
      cd "$test_root"

      stop_guest() {
        if [ -z "$qemu_pid" ]; then
          qemu_pid=$(cat "$qemu_pid_file" 2>/dev/null || true)
        fi
        if ! [[ "$qemu_pid" =~ ^[0-9]+$ ]] \
          || [[ "$(readlink "/proc/$qemu_pid/exe" 2>/dev/null || true)" != *qemu-system* ]]; then
          qemu_pid=
          for process_dir in /proc/[0-9]*; do
            candidate_qemu_pid=''${process_dir##*/}
            if [[ "$(readlink "$process_dir/exe" 2>/dev/null || true)" == *qemu-system* ]] \
              && [[ "$(tr '\0' ' ' < "$process_dir/cmdline" 2>/dev/null || true)" == *"unix:$qmp_socket,server,nowait"* ]]; then
              qemu_pid=$candidate_qemu_pid
              break
            fi
          done
        fi
        if [ -n "$qemu_pid" ]; then
          kill -TERM "$qemu_pid" 2>/dev/null || true
          for ((attempt = 1; attempt <= 50; attempt++)); do
            if ! kill -0 "$qemu_pid" 2>/dev/null; then
              break
            fi
            sleep 0.1
          done
          if kill -0 "$qemu_pid" 2>/dev/null; then
            kill -KILL "$qemu_pid" 2>/dev/null || true
            for ((attempt = 1; attempt <= 50; attempt++)); do
              if ! kill -0 "$qemu_pid" 2>/dev/null; then
                break
              fi
              sleep 0.1
            done
          fi
        fi
        if [ -n "$launch_pid" ] && kill -0 "$launch_pid" 2>/dev/null; then
          kill -KILL "$launch_pid" 2>/dev/null || true
        fi
        if [ -n "$launch_pid" ] && kill -0 "$launch_pid" 2>/dev/null; then
          wait "$launch_pid" 2>/dev/null || true
        fi
        launch_pid=
        qemu_pid=
      }

      shutdown_guest() {
        if [ -z "$launch_pid" ] || ! kill -0 "$launch_pid" 2>/dev/null; then
          echo "guest launch exited before shutdown" >&2
          return 1
        fi

        run_guest_script ${./local-overlay-store/poweroff.sh} >/dev/null 2>&1 || true
        # QEMU exits before virtie asks it to quit, so the launcher currently
        # reports a broken QMP pipe even after an orderly guest poweroff.
        wait "$launch_pid" 2>/dev/null || true
        launch_pid=
        if [[ "$qemu_pid" =~ ^[0-9]+$ ]] \
          && [[ "$(readlink "/proc/$qemu_pid/exe" 2>/dev/null || true)" == *qemu-system* ]]; then
          echo "QEMU remained alive after guest shutdown" >&2
          return 1
        fi
        qemu_pid=
      }

      cleanup() {
        status=$?
        stop_guest
        if [ "$status" -ne 0 ]; then
          echo "local-overlay-store-real-boot: failed with status $status" >&2
          for log in "$non_root_log" "$populate_log" "$reboot_gc_log"; do
            if [ -f "$log" ]; then
              echo "== $log ==" >&2
              tail -n 200 "$log" >&2
            fi
          done
        fi
        rm -r -- "$test_root"
      }
      trap cleanup EXIT

      start_guest() {
        vm_name=$1
        launcher=$2
        log=$3
        vm_dir="$test_root/$vm_name"
        manifest_path="$vm_dir/virtie-agent-sandbox.toml"
        control_socket="$vm_dir/virtie.sock"
        qemu_pid_file="$vm_dir/agent-sandbox.pid"
        qmp_socket="$vm_dir/qmp.sock"
        qemu_pid=
        "$launcher" >"$log" 2>&1 &
        launch_pid=$!

        for ((attempt = 1; attempt <= guest_ready_attempts; attempt++)); do
          candidate_qemu_pid=$(cat "$qemu_pid_file" 2>/dev/null || true)
          if [[ "$candidate_qemu_pid" =~ ^[0-9]+$ ]] \
            && [[ "$(readlink "/proc/$candidate_qemu_pid/exe" 2>/dev/null || true)" == *qemu-system* ]]; then
            qemu_pid=$candidate_qemu_pid
          fi
          if [ -S "$control_socket" ] \
            && ${virtiePackage}/bin/virtie --manifest="$manifest_path" rpc guest-ps \
              >/dev/null 2>&1 \
            && run_guest_script ${./local-overlay-store/ping-daemon.sh} >/dev/null 2>&1; then
            return 0
          fi
          if ! kill -0 "$launch_pid" 2>/dev/null; then
            echo "guest launch exited before the guest agent became ready; see $log" >&2
            return 1
          fi
          sleep "$guest_ready_poll_seconds"
        done

        echo "timed out waiting for the guest agent; see $log" >&2
        return 1
      }

      guest_read_file() {
        guest_path=$1
        request=$(jq -cn --arg path "$guest_path" '{path: $path}')
        if ! response=$(${virtiePackage}/bin/virtie \
          --manifest="$manifest_path" rpc guest-read "$request" 2>/dev/null); then
          return 1
        fi
        jq -er '.["data-base64"] | @base64d' <<<"$response"
      }

      run_guest_script() {
        guest_script_path=$1
        guest_arg=''${2-}
        # virtie's guest-exec RPC has a 500 ms command timeout. Start the real
        # command asynchronously, then collect its status and output through
        # guest-read so store operations can take as long as they need.
        # shellcheck disable=SC2016 # This wrapper is evaluated in the guest.
        guest_wrapper='
          status=/tmp/agentspace-command.status
          stdout=/tmp/agentspace-command.stdout
          stderr=/tmp/agentspace-command.stderr
          rm -f "$status"
          : > "$stdout"
          : > "$stderr"
          (
            /run/current-system/sw/bin/sh "$1" "$2" >"$stdout" 2>"$stderr"
            code=$?
            printf "%s\n" "$code" > "$status"
          ) </dev/null >/dev/null 2>&1 &
        '
        request=$(jq -cn \
          --arg wrapper "$guest_wrapper" \
          --arg script "$guest_script_path" \
          --arg guest_arg "$guest_arg" \
          '{
            path: "/run/current-system/sw/bin/sh",
            args: ["-c", $wrapper, "guest-wrapper", $script, $guest_arg],
            captureOutput: true
          }')
        if ! response=$(${virtiePackage}/bin/virtie \
          --manifest="$manifest_path" rpc guest-exec "$request"); then
          return 1
        fi
        if ! exit_code=$(jq -er '.exitCode' <<<"$response"); then
          return 1
        fi
        if [ "$exit_code" -ne 0 ]; then
          jq -r '(.errData // "") | @base64d' <<<"$response" >&2 || true
          return 1
        fi

        exit_code=
        for ((attempt = 1; attempt <= guest_command_attempts; attempt++)); do
          if exit_code=$(guest_read_file /tmp/agentspace-command.status) \
            && [[ "$exit_code" =~ ^[0-9]+$ ]]; then
            break
          fi
          exit_code=
          if [ -z "$launch_pid" ] || ! kill -0 "$launch_pid" 2>/dev/null; then
            echo "guest launch exited while a command was running" >&2
            return 1
          fi
          sleep "$guest_ready_poll_seconds"
        done
        if [ -z "$exit_code" ]; then
          echo "timed out waiting for a guest command" >&2
          return 1
        fi
        if ! guest_stdout=$(guest_read_file /tmp/agentspace-command.stdout) \
          || ! guest_stderr=$(guest_read_file /tmp/agentspace-command.stderr); then
          echo "could not read guest command output" >&2
          return 1
        fi
        if [ "$exit_code" -ne 0 ]; then
          printf '%s\n' "$guest_stderr" >&2
          return "$exit_code"
        fi
        printf '%s\n' "$guest_stdout"
      }

      check_non_root_daemon() {
        echo "local-overlay-store-real-boot: checking non-root daemon"
        start_guest non-root-vm ${nonRootLaunchScript} "$non_root_log"
        run_guest_script ${./local-overlay-store/non-root-daemon.sh} >/dev/null
        shutdown_guest
      }

      check_reboot_and_gc() {
        echo "local-overlay-store-real-boot: populating upper store"
        start_guest persistent-vm ${persistentLaunchScript} "$populate_log"
        populate_output=$(run_guest_script ${./local-overlay-store/populate-upper.sh})
        shutdown_guest

        canary_path=$(printf '%s\n' "$populate_output" | sed -n 's/^CANARY_PATH=//p' | tail -n 1)
        case "$canary_path" in
          /nix/store/*-agentspace-local-overlay-canary) ;;
          *)
            echo "could not recover canary path from first boot: $canary_path" >&2
            exit 1
            ;;
        esac

        echo "local-overlay-store-real-boot: checking reboot and garbage collection"
        start_guest persistent-vm ${persistentLaunchScript} "$reboot_gc_log"
        run_guest_script ${./local-overlay-store/verify-reboot-and-gc.sh} "$canary_path" >/dev/null
        shutdown_guest
      }

      check_non_root_daemon
      check_reboot_and_gc

      echo "local-overlay-store-real-boot: passed"
    '';
  };
}
