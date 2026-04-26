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

        while [ "$#" -gt 0 ]; do
          case "$1" in
            -qmp)
              shift
              qmp_socket="$1"
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

        export QMP_SOCKET="$qmp_socket"
        export QEMU_PARENT_PID="$$"
        ${pkgs.python3}/bin/python - <<'PY' &
import json
import os
import signal
import socket
import threading

socket_path = os.environ["QMP_SOCKET"]
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
status_lock = threading.Lock()
accepted_connections = 0
accept_lock = threading.Lock()

def touch(name):
    with open(os.path.join(state_dir, name), "a", encoding="utf-8"):
        pass

def handle(conn):
    global status
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
            elif command == "quit":
                send({"return": {}})
                os.kill(parent_pid, signal.SIGTERM)
                return
            else:
                send({"return": {}})
    conn.close()

def hold_second_client(conn):
    try:
        while conn.recv(4096):
            pass
    finally:
        conn.close()

while True:
    conn, _ = server.accept()
    with accept_lock:
        accepted_connections += 1
        connection_number = accepted_connections
    if connection_number > 1:
        touch("qmp-second-client")
        threading.Thread(target=hold_second_client, args=(conn,), daemon=True).start()
        continue
    threading.Thread(target=handle, args=(conn,), daemon=True).start()
PY
        qmp_pid=$!

        cleanup() {
          trap - EXIT INT TERM
          kill "$qmp_pid" 2>/dev/null || true
          wait "$qmp_pid" 2>/dev/null || true
          rm -f "$qmp_socket"
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

    exec "''${remote_command[@]}"
  '';

  manifest = pkgs.writeText "virtie-fake-manifest.json" (
    builtins.toJSON {
      identity.hostName = "virtie-fake";
      paths = {
        workingDir = ".";
        lockPath = "virtie.lock";
        runtimeDir = "";
      };
      persistence.directories = [ "state" ];
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
      if [ -n "''${launch_pid:-}" ]; then
        kill "$launch_pid" 2>/dev/null || true
        kill -CONT "$launch_pid" 2>/dev/null || true
        wait "$launch_pid" 2>/dev/null || true
      fi
      rm -rf "$tmpdir"
    }
    trap cleanup EXIT INT TERM

    mkdir -p "$workspace_dir"
    cd "$workspace_dir"

    export PATH=${fakeTools}/bin:$PATH
    export XDG_RUNTIME_DIR="$tmpdir/run"
    mkdir -p "$XDG_RUNTIME_DIR"

    if ! ${launchScript} sh -c 'test -f state/virtiofsd-started; test -f state/qemu-started; test -f .virtie/virtie-fake.pid; test -f "$XDG_RUNTIME_DIR/agentspace/virtie-fake/virtiofs.sock"; test -f "$XDG_RUNTIME_DIR/agentspace/virtie-fake/virtiofs.sock.pid"; test -S "$XDG_RUNTIME_DIR/agentspace/virtie-fake/qmp.sock"; test -f overlay.img; test ! -e virtiofs.sock; test ! -e virtiofs.sock.pid; test ! -e qmp.sock; echo AGENTSPACE_VIRTIE_OK' >"$launch_log" 2>&1; then
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
    test ! -e "$workspace_dir/.virtie/virtie-fake.pid"

    pause_workspace_dir="$tmpdir/pause-workspace"
    pause_log="$tmpdir/virtie-pause.log"
    mkdir -p "$pause_workspace_dir"
    cd "$pause_workspace_dir"

    ${launchScript} sh -c 'while [ ! -f state/resume-finished ]; do sleep 0.1; done' >"$pause_log" 2>&1 &
    launch_pid=$!

    for _ in $(seq 1 100); do
      if [ -S "$XDG_RUNTIME_DIR/agentspace/virtie-fake/qmp.sock" ] && [ -f .virtie/virtie-fake.pid ] && [ -f state/ssh-destination ]; then
        break
      fi
      sleep 0.1
    done

    if [ ! -S "$XDG_RUNTIME_DIR/agentspace/virtie-fake/qmp.sock" ]; then
      echo "virtie-launch-e2e: qmp socket did not appear for suspend/resume case" >&2
      cat "$pause_log" >&2
      exit 1
    fi

    ${virtiePackage}/bin/virtie suspend --manifest=${manifest}
    test -f "$pause_workspace_dir/state/qemu-paused"
    test ! -e "$pause_workspace_dir/state/qmp-second-client"
    test -f "$pause_workspace_dir/.virtie/virtie-fake.pid"
    test -f "$pause_workspace_dir/.virtie/virtie-fake.suspend.json"
    grep -F '"status": "paused"' "$pause_workspace_dir/.virtie/virtie-fake.suspend.json" >/dev/null

    ${virtiePackage}/bin/virtie resume --manifest=${manifest}
    test -f "$pause_workspace_dir/state/qemu-resumed"
    test ! -e "$pause_workspace_dir/state/qmp-second-client"
    test -f "$pause_workspace_dir/.virtie/virtie-fake.pid"
    test ! -e "$pause_workspace_dir/.virtie/virtie-fake.suspend.json"

    touch "$pause_workspace_dir/state/resume-finished"
    if ! wait "$launch_pid"; then
      echo "virtie-launch-e2e: suspend/resume launch exited non-zero" >&2
      cat "$pause_log" >&2
      exit 1
    fi
    test ! -e "$pause_workspace_dir/.virtie/virtie-fake.pid"

    mkdir -p "$out"
    cp "$launch_log" "$out/virtie.log"
    cp "$pause_log" "$out/virtie-pause.log"
  '';
}
