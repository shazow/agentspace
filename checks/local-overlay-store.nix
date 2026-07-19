{
  mkLaunch,
  mkSandbox,
  pkgs,
  virtiePackage,
  ...
}:
let
  localOverlayStoreEnabled = true;
  vmConfig = {
    localOverlayStore.enable = localOverlayStoreEnabled;
    ssh.autoconnect = false;
    quiet = false;
    machine = {
      memory = 1024;
      vcpu = 1;
    };
    persistence = {
      baseDir = "vm";
      homeImage = null;
      storeDisk = false;
      storeOverlay = "overlay.img";
    };
    workspace.enable = false;
    extraModules = [
      {
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
      }
    ];
  };
  vm = mkSandbox vmConfig;
  nonRootVm = mkSandbox (
    vmConfig
    // {
      persistence = vmConfig.persistence // {
        baseDir = "non-root-vm";
      };
      extraModules = vmConfig.extraModules ++ [
        {
          users.groups.nix-daemon = { };
          users.users.nix-daemon = {
            isSystemUser = true;
            group = "nix-daemon";
          };
          nix = {
            daemonUser = "nix-daemon";
            daemonGroup = "nix-daemon";
            settings.experimental-features = [ "auto-allocate-uids" ];
          };
        }
      ];
    }
  );
  launchScript = mkLaunch vm;
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
      first_log="$test_root/first-boot.log"
      second_log="$test_root/second-boot.log"
      manifest_path=
      control_socket=
      qemu_pid_file=
      qmp_socket=
      launch_pid=
      qemu_pid=

      mkdir -p "$HOME" "$XDG_RUNTIME_DIR"
      chmod 700 "$XDG_RUNTIME_DIR"
      cd "$test_root"

      select_vm() {
        vm_dir="$test_root/$1"
        manifest_path="$vm_dir/virtie-agent-sandbox.toml"
        control_socket="$vm_dir/virtie.sock"
        qemu_pid_file="$vm_dir/agent-sandbox.pid"
        qmp_socket="$vm_dir/qmp.sock"
      }

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

        run_guest_command \
          '/run/current-system/sw/bin/systemctl --no-block poweroff' \
          >/dev/null 2>&1 || true
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
          for log in "$non_root_log" "$first_log" "$second_log"; do
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
        log=$1
        launcher=$2
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
            && run_guest_command \
              'export NIX_REMOTE=daemon; /run/current-system/sw/bin/nix store ping' \
              >/dev/null 2>&1; then
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

      run_guest_command() {
        guest_script=$1
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
            /run/current-system/sw/bin/sh -c "$1" guest-script "$2" >"$stdout" 2>"$stderr"
            code=$?
            printf "%s\n" "$code" > "$status"
          ) </dev/null >/dev/null 2>&1 &
        '
        request=$(jq -cn \
          --arg wrapper "$guest_wrapper" \
          --arg script "$guest_script" \
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

      # shellcheck disable=SC2016 # This script is evaluated in the guest.
      non_root_script='
        set -eu

        export NIX_REMOTE=daemon
        nix=/run/current-system/sw/bin/nix
        payload=/tmp/agentspace-non-root-overlay-canary
        printf "%s\n" agentspace-non-root-overlay-canary > "$payload"
        canary_path=$($nix store add-file "$payload")

        for writable_path in \
          /nix/.rw-store/state \
          /nix/.local-overlay-lower-store \
          /nix/.local-overlay-lower-store/.links; do
          if [ "$(stat -c "%U:%G" "$writable_path")" != nix-daemon:nix-daemon ]; then
            echo "$writable_path is not owned by nix-daemon:nix-daemon" >&2
            exit 1
          fi
        done

        test -e "$canary_path"
        test -e "/nix/.rw-store/store/''${canary_path##*/}"
        $nix path-info "$canary_path"
      '

      select_vm non-root-vm
      start_guest "$non_root_log" ${nonRootLaunchScript}
      run_guest_command "$non_root_script" >/dev/null
      shutdown_guest

      # shellcheck disable=SC2016 # This script is evaluated in the guest.
      first_script='
        set -eu

        export NIX_REMOTE=daemon
        nix=/run/current-system/sw/bin/nix
        payload=/tmp/agentspace-local-overlay-canary
        printf "%s\n" agentspace-local-overlay-canary > "$payload"
        canary_path=$($nix store add-file "$payload")
        upper_path=/nix/.rw-store/store/''${canary_path##*/}

        test -e "$canary_path"
        test -e "$upper_path"
        $nix path-info "$canary_path"
        sync
        printf "CANARY_PATH=%s\n" "$canary_path"
      '

      select_vm vm
      start_guest "$first_log" ${launchScript}
      first_output=$(run_guest_command "$first_script")
      shutdown_guest

      canary_path=$(printf '%s\n' "$first_output" | sed -n 's/^CANARY_PATH=//p' | tail -n 1)
      case "$canary_path" in
        /nix/store/*-agentspace-local-overlay-canary) ;;
        *)
          echo "could not recover canary path from first boot: $canary_path" >&2
          exit 1
          ;;
      esac

      # shellcheck disable=SC2016 # This script is evaluated in the guest.
      second_script='
        set -u

        export NIX_REMOTE=daemon
        nix=/run/current-system/sw/bin/nix
        canary_path=$1
        canary_name=''${canary_path##*/}
        upper_path=/nix/.rw-store/store/$canary_name
        deleted_paths=/tmp/agentspace-minimal-gc-deletions
        lower_probe=$(readlink -f /run/current-system/sw/bin/sh)
        lower_name=''${lower_probe#/nix/store/}
        lower_name=''${lower_name%%/*}
        lower_physical=/nix/.ro-store/$lower_name

        snapshot_upper() {
          find /nix/.rw-store/store -mindepth 1 -maxdepth 1 \
            ! -name "$canary_name" -printf "%f:%y\n" | sort
        }

        case "$lower_probe" in
          /nix/store/*) ;;
          *)
            echo "could not resolve a lower store probe: $lower_probe" >&2
            exit 1
            ;;
        esac
        if [ ! -e "$lower_physical" ] || [ ! -e "/nix/store/$lower_name" ]; then
          echo "lower store probe is not visible before garbage collection" >&2
          exit 1
        fi

        upper_before=$(snapshot_upper)
        if ! $nix path-info "$canary_path"; then
          echo "metadata for the upper store path did not survive reboot" >&2
          exit 1
        fi

        if ! minimal_gc_output=$($nix store gc --max 1M 2>&1); then
          printf "%s\n" "$minimal_gc_output" >&2
          echo "bounded guest store garbage collection failed" >&2
          exit 1
        fi
        printf "%s\n" "$minimal_gc_output" >&2

        printf "%s\n" "$minimal_gc_output" \
          | sed -n "s|^deleting .\(/nix/store/.*\).$|\1|p" \
          > "$deleted_paths"
        while IFS= read -r deleted_path; do
          deleted_name=''${deleted_path#/nix/store/}
          deleted_name=''${deleted_name%%/*}
          if [ -e "/nix/.ro-store/$deleted_name" ] \
            && [ ! -e "/nix/store/$deleted_name" ]; then
            echo "bounded garbage collection hid lower path $deleted_path" >&2
            exit 1
          fi
        done < "$deleted_paths"

        upper_after_bounded=$(snapshot_upper)
        if [ "$upper_before" != "$upper_after_bounded" ]; then
          echo "bounded garbage collection made unexpected changes to the upper layer" >&2
          echo "upper entries before GC, excluding the canary:" >&2
          printf "%s\n" "$upper_before" | sed "s/^/  /" >&2
          echo "upper entries after GC:" >&2
          printf "%s\n" "$upper_after_bounded" | sed "s/^/  /" >&2
          exit 1
        fi
        if [ ! -e "$lower_physical" ] || [ ! -e "/nix/store/$lower_name" ]; then
          echo "bounded garbage collection hid the lower store probe" >&2
          exit 1
        fi

        if ! $nix store gc; then
          echo "full guest store garbage collection failed" >&2
          exit 1
        fi

        upper_after_full=$(snapshot_upper)
        if [ "$upper_before" != "$upper_after_full" ]; then
          echo "full garbage collection made unexpected changes to the upper layer" >&2
          echo "upper entries before GC, excluding the canary:" >&2
          printf "%s\n" "$upper_before" | sed "s/^/  /" >&2
          echo "upper entries after GC:" >&2
          printf "%s\n" "$upper_after_full" | sed "s/^/  /" >&2
          exit 1
        fi
        if [ ! -e "$lower_physical" ] || [ ! -e "/nix/store/$lower_name" ]; then
          echo "full garbage collection hid the lower store probe" >&2
          exit 1
        fi

        if [ -e "$canary_path" ] || [ -e "$upper_path" ]; then
          echo "garbage collection left the unrooted upper store path behind" >&2
          exit 1
        fi

        if $nix path-info "$canary_path" >/dev/null 2>&1; then
          echo "garbage collection left the canary metadata registered" >&2
          exit 1
        fi

        sync
      '

      start_guest "$second_log" ${launchScript}
      run_guest_command "$second_script" "$canary_path" >/dev/null
      shutdown_guest

      echo "local-overlay-store-real-boot: passed"
    '';
  };
}
