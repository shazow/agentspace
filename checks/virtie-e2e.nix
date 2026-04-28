{
  mkLaunch,
  pkgs,
  virtiePackage,
  ...
}:
let
  fakeTools = pkgs.symlinkJoin {
    name = "virtie-fake-tools";
    paths = [
      (pkgs.writeShellScriptBin "qemu-system-x86_64" ''
        set -euo pipefail

        mkdir -p "$PWD/state"
        qmp_socket=""
        qga_socket=""
        incoming=""

        while [ "$#" -gt 0 ]; do
          case "$1" in
            -qmp)
              shift
              qmp_socket="$1"
              ;;
            -chardev)
              shift
              chardev_spec="$1"
              case "$chardev_spec" in
                socket,*id=qga0*|socket,*id=qga0)
                  IFS=',' read -r -a chardev_parts <<< "$chardev_spec"
                  for chardev_part in "''${chardev_parts[@]}"; do
                    case "$chardev_part" in
                      path=*) qga_socket="''${chardev_part#path=}" ;;
                    esac
                  done
                  ;;
              esac
              ;;
            -incoming)
              shift
              incoming="$1"
              ;;
            *guest-cid=*)
              cid="''${1##*guest-cid=}"
              printf '%s\n' "$cid" > "$PWD/state/qemu-vsock-cid"
              ;;
          esac
          shift || true
        done

        case "$qmp_socket" in
          unix:*,server,nowait)
            qmp_socket="''${qmp_socket#unix:}"
            qmp_socket="''${qmp_socket%,server,nowait}"
            ;;
          "")
            echo "fake qemu: missing -qmp unix socket" >&2
            exit 1
            ;;
          *)
            echo "fake qemu: unsupported qmp socket spec: $qmp_socket" >&2
            exit 1
            ;;
        esac

        touch "$PWD/state/qemu-started"
        if [ "$incoming" = "defer" ]; then
          touch "$PWD/state/qemu-incoming-defer"
        fi

        export QMP_SOCKET="$qmp_socket"
        export QGA_SOCKET="$qga_socket"
        export QEMU_PARENT_PID="$$"
        ${pkgs.python3}/bin/python - <<'PY' &
import json
import os
import signal
import socket
import threading

socket_path = os.environ["QMP_SOCKET"]
qga_socket_path = os.environ.get("QGA_SOCKET", "")
parent_pid = int(os.environ["QEMU_PARENT_PID"])
state_dir = os.path.join(os.getcwd(), "state")

os.makedirs(os.path.dirname(socket_path) or ".", exist_ok=True)
try:
    os.unlink(socket_path)
except FileNotFoundError:
    pass

server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
server.bind(socket_path)
server.listen(16)
status = "running"
migration_status = "none"
status_lock = threading.Lock()
client_count = 0
qga_handles = {}
qga_next_handle = 0
qga_next_pid = 1000
qga_dirs = {"/", "/etc"}
qga_exec_statuses = {}

def touch(name):
    with open(os.path.join(state_dir, name), "a", encoding="utf-8"):
        pass

def handle(conn):
    global status
    global migration_status
    def send(message):
        conn.sendall(json.dumps(message).encode("utf-8") + b"\r\n")

    send(
        {
            "QMP": {
                "version": {
                    "qemu": {"major": 8, "minor": 2, "micro": 0},
                    "package": "",
                },
                "capabilities": [],
            }
        }
    )

    decoder = json.JSONDecoder()
    buffer = ""
    while True:
        chunk = conn.recv(4096)
        if not chunk:
            break
        buffer += chunk.decode("utf-8")
        while True:
            stripped = buffer.lstrip()
            if not stripped:
                buffer = ""
                break
            try:
                message, consumed = decoder.raw_decode(stripped)
            except json.JSONDecodeError:
                break
            buffer = stripped[consumed:]
            command = message.get("execute")
            if command == "query-status":
                with status_lock:
                    current_status = status
                send(
                    {
                        "return": {
                            "running": current_status == "running",
                            "singlestep": False,
                            "status": current_status,
                        }
                    }
                )
            elif command == "stop":
                with status_lock:
                    status = "paused"
                touch("qemu-paused")
                send({"return": {}})
            elif command == "cont":
                with status_lock:
                    status = "running"
                touch("qemu-resumed")
                send({"return": {}})
            elif command == "migrate":
                uri = message.get("arguments", {}).get("uri", "")
                if uri.startswith("file:"):
                    with open(uri.removeprefix("file:"), "w", encoding="utf-8") as state_file:
                        state_file.write("fake migration state\n")
                with status_lock:
                    migration_status = "completed"
                touch("qemu-migrated")
                send({"return": {}})
            elif command == "migrate-incoming":
                uri = message.get("arguments", {}).get("uri", "")
                if uri.startswith("file:") and not os.path.exists(uri.removeprefix("file:")):
                    send({"error": {"class": "GenericError", "desc": "missing migration file"}})
                    continue
                with status_lock:
                    migration_status = "completed"
                touch("qemu-migrate-incoming")
                send({"return": {}})
            elif command == "query-migrate":
                with status_lock:
                    current_migration_status = migration_status
                send({"return": {"status": current_migration_status}})
            elif command == "quit":
                send({"return": {}})
                os.kill(parent_pid, signal.SIGTERM)
                return
            else:
                send({"return": {}})
    conn.close()

def qga_handle(conn):
    global qga_next_handle
    global qga_next_pid
    decoder = json.JSONDecoder()
    buffer = ""

    def send(message):
        conn.sendall(json.dumps(message).encode("utf-8") + b"\r\n")

    while True:
        chunk = conn.recv(4096)
        if not chunk:
            break
        buffer += chunk.decode("utf-8")
        while True:
            stripped = buffer.lstrip()
            if not stripped:
                buffer = ""
                break
            try:
                message, consumed = decoder.raw_decode(stripped)
            except json.JSONDecodeError:
                break
            buffer = stripped[consumed:]
            command = message.get("execute")
            args = message.get("arguments", {})
            if command == "guest-ping":
                touch("guest-agent-ping")
                send({"return": {}})
            elif command == "guest-file-open":
                qga_next_handle += 1
                qga_handles[qga_next_handle] = args.get("path", "")
                send({"return": qga_next_handle})
            elif command == "guest-file-write":
                handle = args.get("handle")
                path = qga_handles.get(handle, "")
                payload = args.get("buf-b64", "")
                with open(os.path.join(state_dir, "guest-agent-writes"), "a", encoding="utf-8") as writes:
                    writes.write(f"{path} {payload}\n")
                send({"return": {"count": len(payload), "eof": False}})
            elif command == "guest-file-close":
                handle = args.get("handle")
                path = qga_handles.pop(handle, "")
                with open(os.path.join(state_dir, "guest-agent-closes"), "a", encoding="utf-8") as closes:
                    closes.write(f"{path}\n")
                send({"return": {}})
            elif command == "guest-exec":
                qga_next_pid += 1
                path = args.get("path", "")
                argv = args.get("arg", [])
                capture_output = args.get("capture-output", False)
                status = {"exited": True, "exitcode": 0}
                if path == "/run/current-system/sw/bin/test" and len(argv) == 2 and argv[0] == "-d":
                    status = {"exited": True, "exitcode": 0 if argv[1] in qga_dirs else 1}
                elif path == "/run/current-system/sw/bin/install" and len(argv) >= 2 and argv[0] == "-d":
                    qga_dirs.add(argv[-1])
                qga_exec_statuses[qga_next_pid] = status
                with open(os.path.join(state_dir, "guest-agent-execs"), "a", encoding="utf-8") as execs:
                    execs.write(f"{path} {' '.join(argv)} capture-output={capture_output}\n")
                send({"return": {"pid": qga_next_pid}})
            elif command == "guest-exec-status":
                send({"return": qga_exec_statuses.get(args.get("pid"), {"exited": True, "exitcode": 0})})
            else:
                send({"return": {}})
    conn.close()

def qga_serve():
    if not qga_socket_path:
        return
    os.makedirs(os.path.dirname(qga_socket_path) or ".", exist_ok=True)
    try:
        os.unlink(qga_socket_path)
    except FileNotFoundError:
        pass
    qga_server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    qga_server.bind(qga_socket_path)
    qga_server.listen(16)
    while True:
        conn, _ = qga_server.accept()
        threading.Thread(target=qga_handle, args=(conn,), daemon=True).start()

threading.Thread(target=qga_serve, daemon=True).start()

while True:
    conn, _ = server.accept()
    with status_lock:
        client_count += 1
        current_client_count = client_count
    if current_client_count > 1:
        touch("qmp-second-client")
        conn.close()
        continue
    threading.Thread(target=handle, args=(conn,), daemon=True).start()
PY
        qmp_pid=$!

        cleanup() {
          trap - EXIT INT TERM
          kill "$qmp_pid" 2>/dev/null || true
          wait "$qmp_pid" 2>/dev/null || true
          rm -f "$qmp_socket" "$qga_socket"
          touch "$PWD/state/qemu-stopped"
          exit 0
        }
        trap cleanup EXIT INT TERM

        wait "$qmp_pid" || true
        cleanup
      '')
      (pkgs.writeShellScriptBin "virtiofsd-workspace" ''
        set -euo pipefail

        mkdir -p "$PWD/state"
        socket_path="''${VIRTIE_SOCKET_PATH:-$PWD/virtiofs.sock}"
        pid_path="$socket_path.pid"
        mkdir -p "$(dirname "$socket_path")"
        touch "$PWD/state/virtiofsd-started"
        : > "$socket_path"
        printf '%s\n' "$$" > "$pid_path"

        cleanup() {
          trap - EXIT INT TERM
          rm -f "$socket_path" "$pid_path"
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

    if [ "''${#remote_command[@]}" -eq 0 ]; then
      touch "$PWD/state/ssh-session-started"
      while [ ! -f "$PWD/state/ssh-session-finished" ]; do
        sleep 0.1
      done
      exit 0
    fi

    command_string="''${remote_command[*]}"
    printf '%s\n' "$command_string" > "$PWD/state/ssh-remote-command"
    exec ${pkgs.runtimeShell} -c "$command_string"
  '';

  manifest = pkgs.writeText "virtie-fake-manifest.json" (
    builtins.toJSON {
      identity.hostName = "virtie-fake";
      paths = {
        workingDir = ".";
        lockPath = "virtie.lock";
        runtimeDir = "";
      };
      persistence = {
        directories = [ "state" ];
        baseDir = ".agentspace";
        stateDir = ".agentspace";
      };
      ssh = {
        argv = [ fakeSSH ];
        user = "agent";
      };
      qemu = {
        binaryPath = "${fakeTools}/bin/qemu-system-x86_64";
        name = "virtie-fake";
        machine = {
          type = "microvm";
          options = [ "accel=tcg" ];
        };
        cpu = {
          model = "host";
        };
        memory = {
          sizeMiB = 256;
          backend = "default";
        };
        kernel = {
          path = "/tmp/vmlinuz";
          initrdPath = "/tmp/initrd";
          params = "panic=-1";
        };
        smp = {
          cpus = 2;
        };
        console = {
          stdioChardev = true;
        };
        knobs = {
          noDefaults = true;
          noUserConfig = true;
          noReboot = true;
          noGraphic = true;
        };
        qmp = {
          socketPath = "qmp.sock";
        };
        guestAgent = {
          socketPath = "qga.sock";
        };
        devices = {
          rng = {
            id = "rng0";
            transport = "pci";
          };
          virtiofs = [
            {
              id = "fs0";
              socketPath = "virtiofs.sock";
              tag = "workspace";
              transport = "pci";
            }
          ];
          block = [
            {
              id = "vda";
              imagePath = "overlay.img";
              aio = "threads";
              transport = "pci";
            }
          ];
          network = [
            {
              id = "net0";
              backend = "user";
              macAddress = "02:02:00:00:00:01";
              transport = "pci";
            }
          ];
          vsock = {
            id = "vsock0";
            transport = "pci";
          };
        };
      };
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
      writeFiles = {
        "/etc/virtie/inline" = {
          chown = "agent:users";
          content = "aW5saW5lLWZyb20tbWFuaWZlc3Q=";
          mode = "0640";
        };
        "/var/lib/virtie/host" = {
          path = "host-write-file";
        };
      };
    }
  );

  launchScript = mkLaunch {
    config = {
      agentspace.sandbox.command = "";
      agentspace.sandbox.launch = {
        commonInit = ''
          cd "$REPO_DIR"
        '';
        virtieManifest = ".agentspace/virtie-fake.json";
        virtieManifestTemplate = manifest;
      };
    };
  };

  commandLaunchScript = mkLaunch {
    config = {
      agentspace.sandbox.command = ''
        sh -c 'printf "%s\n" "configured value with spaces" > state/configured-command'
      '';
      agentspace.sandbox.launch = {
        commonInit = ''
          cd "$REPO_DIR"
        '';
        virtieManifest = ".agentspace/virtie-fake.json";
        virtieManifestTemplate = manifest;
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
    failed=0
    trap 'echo "virtie-launch-e2e: command failed at line $LINENO" >&2' ERR

    cleanup() {
      status=$?
      if [ "$status" -ne 0 ] && [ "$failed" -eq 0 ]; then
        failed=1
        echo "virtie-launch-e2e: failed with status $status" >&2
        for log in "$tmpdir"/*.log; do
          if [ -f "$log" ]; then
            echo "== $log ==" >&2
            cat "$log" >&2
          fi
        done
        for manifest_file in "$tmpdir"/*/.agentspace/virtie-fake.json; do
          if [ -f "$manifest_file" ]; then
            echo "== $manifest_file ==" >&2
            cat "$manifest_file" >&2
          fi
        done
      fi
      if [ -n "''${launch_pid:-}" ]; then
        kill "$launch_pid" 2>/dev/null || true
        wait "$launch_pid" 2>/dev/null || true
      fi
      if [ -n "''${resume_pid:-}" ]; then
        touch "$PWD/state/ssh-session-finished" 2>/dev/null || true
        kill "$resume_pid" 2>/dev/null || true
        wait "$resume_pid" 2>/dev/null || true
      fi
      rm -rf "$tmpdir"
    }
    trap cleanup EXIT INT TERM

    mkdir -p "$workspace_dir"
    cd "$workspace_dir"
    printf '%s' 'host payload' > host-write-file

    export PATH=${fakeTools}/bin:$PATH
    export XDG_RUNTIME_DIR="$tmpdir/run"
    mkdir -p "$XDG_RUNTIME_DIR"

    if ! ${launchScript} sh -c 'test -f state/virtiofsd-started; test -f state/qemu-started; test -f state/guest-agent-ping; test -f .agentspace/virtie-fake.json; test -f .agentspace/virtie-fake.pid; test -f "$XDG_RUNTIME_DIR/agentspace/virtie-fake/virtiofs.sock"; test -f "$XDG_RUNTIME_DIR/agentspace/virtie-fake/virtiofs.sock.pid"; test -S "$XDG_RUNTIME_DIR/agentspace/virtie-fake/qmp.sock"; test -S "$XDG_RUNTIME_DIR/agentspace/virtie-fake/qga.sock"; test -f overlay.img; test ! -e virtiofs.sock; test ! -e virtiofs.sock.pid; test ! -e qmp.sock; test ! -e qga.sock; echo AGENTSPACE_VIRTIE_OK' >"$launch_log" 2>&1; then
      echo "virtie-launch-e2e: launch script exited non-zero" >&2
      cat "$launch_log" >&2
      exit 1
    fi

    grep -F 'AGENTSPACE_VIRTIE_OK' "$launch_log" >/dev/null
    grep -F 'virtie: starting virtiofsd [workspace]' "$launch_log" >/dev/null
    grep -F 'virtie: starting qemu' "$launch_log" >/dev/null
    grep -F 'virtie: allocated vsock cid 3' "$launch_log" >/dev/null
    grep -F 'virtie: wrote guest file /etc/virtie/inline' "$launch_log" >/dev/null
    grep -F 'virtie: wrote guest file /var/lib/virtie/host' "$launch_log" >/dev/null
    workspace_real="$(${pkgs.coreutils}/bin/realpath "$workspace_dir")"
    grep -F '"workingDir": "'"$workspace_real"'"' "$workspace_dir/.agentspace/virtie-fake.json" >/dev/null
    grep -Fx '3' "$workspace_dir/state/qemu-vsock-cid" >/dev/null
    grep -Fx 'agent@vsock/3' "$workspace_dir/state/ssh-destination" >/dev/null
    grep -Fx "$workspace_real/overlay.img" "$workspace_dir/state/mkfs-ext4-args" >/dev/null
    grep -Fx '/etc/virtie/inline aW5saW5lLWZyb20tbWFuaWZlc3Q=' "$workspace_dir/state/guest-agent-writes" >/dev/null
    grep -Fx '/var/lib/virtie/host aG9zdCBwYXlsb2Fk' "$workspace_dir/state/guest-agent-writes" >/dev/null
    grep -Fx '/etc/virtie/inline' "$workspace_dir/state/guest-agent-closes" >/dev/null
    grep -Fx '/var/lib/virtie/host' "$workspace_dir/state/guest-agent-closes" >/dev/null
    grep -Fx '/run/current-system/sw/bin/test -d /etc/virtie capture-output=True' "$workspace_dir/state/guest-agent-execs" >/dev/null
    grep -Fx '/run/current-system/sw/bin/install -d -o agent -g users /etc/virtie capture-output=True' "$workspace_dir/state/guest-agent-execs" >/dev/null
    grep -Fx '/run/current-system/sw/bin/chown agent:users /etc/virtie/inline capture-output=True' "$workspace_dir/state/guest-agent-execs" >/dev/null
    grep -Fx '/run/current-system/sw/bin/chmod 0640 /etc/virtie/inline capture-output=True' "$workspace_dir/state/guest-agent-execs" >/dev/null
    grep -Fx '/run/current-system/sw/bin/test -d /var/lib/virtie capture-output=True' "$workspace_dir/state/guest-agent-execs" >/dev/null
    grep -Fx '/run/current-system/sw/bin/install -d /var/lib/virtie capture-output=True' "$workspace_dir/state/guest-agent-execs" >/dev/null
    test -f "$workspace_dir/state/qemu-stopped"
    test -f "$workspace_dir/state/virtiofsd-stopped"
    test ! -e "$workspace_dir/.agentspace/virtie-fake.pid"

    command_workspace_dir="$tmpdir/command-workspace"
    command_log="$tmpdir/virtie-command.log"
    mkdir -p "$command_workspace_dir"
    cd "$command_workspace_dir"
    printf '%s' 'host payload' > host-write-file

    if ! ${commandLaunchScript} >"$command_log" 2>&1; then
      echo "virtie-launch-e2e: configured command launch exited non-zero" >&2
      cat "$command_log" >&2
      exit 1
    fi
    grep -Fx 'configured value with spaces' "$command_workspace_dir/state/configured-command" >/dev/null
    grep -F "configured value with spaces" "$command_workspace_dir/state/ssh-remote-command" >/dev/null
    test -f "$command_workspace_dir/state/qemu-stopped"
    test -f "$command_workspace_dir/state/virtiofsd-stopped"
    test ! -e "$command_workspace_dir/.agentspace/virtie-fake.pid"

    disk_workspace_dir="$tmpdir/disk-workspace"
    disk_log="$tmpdir/virtie-disk.log"
    disk_resume_log="$tmpdir/virtie-disk-resume.log"
    mkdir -p "$disk_workspace_dir"
    cd "$disk_workspace_dir"
    printf '%s' 'host payload' > host-write-file

    ${launchScript} >"$disk_log" 2>&1 &
    launch_pid=$!

    for _ in $(seq 1 100); do
      if [ -S "$XDG_RUNTIME_DIR/agentspace/virtie-fake/qmp.sock" ] && [ -f .agentspace/virtie-fake.pid ] && [ -f state/ssh-destination ]; then
        break
      fi
      sleep 0.1
    done

    disk_manifest="$disk_workspace_dir/.agentspace/virtie-fake.json"
    ${virtiePackage}/bin/virtie suspend --manifest="$disk_manifest"
    test ! -f "$disk_workspace_dir/state/qmp-second-client"
    test -f "$disk_workspace_dir/state/qemu-paused"
    test -f "$disk_workspace_dir/state/qemu-migrated"
    test -f "$disk_workspace_dir/.agentspace/virtie-fake.vmstate"
    test -f "$disk_workspace_dir/.agentspace/virtie-fake.suspend.json"
    test ! -e "$disk_workspace_dir/.agentspace/virtie-fake.pid"
    grep -F '"status": "saved"' "$disk_workspace_dir/.agentspace/virtie-fake.suspend.json" >/dev/null
    grep -F '"cid": 3' "$disk_workspace_dir/.agentspace/virtie-fake.suspend.json" >/dev/null

    if ! wait "$launch_pid"; then
      echo "virtie-launch-e2e: disk suspend launch exited non-zero" >&2
      cat "$disk_log" >&2
      exit 1
    fi
    unset launch_pid

    disk_resume_cwd="$tmpdir/disk-resume-cwd"
    mkdir -p "$disk_resume_cwd"
    cd "$disk_resume_cwd"

    ${virtiePackage}/bin/virtie launch --resume=force --manifest="$disk_manifest" >"$disk_resume_log" 2>&1 &
    resume_pid=$!
    for _ in $(seq 1 100); do
      if [ -f "$disk_workspace_dir/state/qemu-migrate-incoming" ] && [ -f "$disk_workspace_dir/state/qemu-resumed" ] && [ -f "$disk_workspace_dir/state/ssh-session-started" ]; then
        break
      fi
      sleep 0.1
    done

    test -f "$disk_workspace_dir/state/qemu-incoming-defer"
    test -f "$disk_workspace_dir/state/qemu-migrate-incoming"
    test -f "$disk_workspace_dir/state/qemu-resumed"
    grep -Fx '3' "$disk_workspace_dir/state/qemu-vsock-cid" >/dev/null
    test ! -e "$disk_workspace_dir/.agentspace/virtie-fake.vmstate"
    test ! -e "$disk_workspace_dir/.agentspace/virtie-fake.suspend.json"
    test -f "$disk_workspace_dir/.agentspace/virtie-fake.pid"

    touch "$disk_workspace_dir/state/ssh-session-finished"
    if ! wait "$resume_pid"; then
      echo "virtie-launch-e2e: disk resume exited non-zero" >&2
      cat "$disk_resume_log" >&2
      exit 1
    fi
    unset resume_pid
    test ! -e "$disk_workspace_dir/.agentspace/virtie-fake.pid"

    mkdir -p "$out"
    cp "$launch_log" "$out/virtie.log"
    cp "$command_log" "$out/virtie-command.log"
    cp "$disk_log" "$out/virtie-disk.log"
    cp "$disk_resume_log" "$out/virtie-disk-resume.log"
  '';
}
