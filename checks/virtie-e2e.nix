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

socket_path = os.environ["QMP_SOCKET"]
parent_pid = int(os.environ["QEMU_PARENT_PID"])

os.makedirs(os.path.dirname(socket_path) or ".", exist_ok=True)
try:
    os.unlink(socket_path)
except FileNotFoundError:
    pass

server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
server.bind(socket_path)
server.listen(1)
conn, _ = server.accept()

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
quit_requested = False
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
        send({"return": {}})
        if command == "quit":
            os.kill(parent_pid, signal.SIGTERM)
            buffer = ""
            quit_requested = True
            break
    if quit_requested:
        break

conn.close()
server.close()
try:
    os.unlink(socket_path)
except FileNotFoundError:
    pass
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
      rm -rf "$tmpdir"
    }
    trap cleanup EXIT INT TERM

    mkdir -p "$workspace_dir"
    cd "$workspace_dir"

    export PATH=${fakeTools}/bin:$PATH
    export XDG_RUNTIME_DIR="$tmpdir/run"
    mkdir -p "$XDG_RUNTIME_DIR"

    if ! ${launchScript} sh -c 'test -f state/virtiofsd-started; test -f state/qemu-started; test -f "$XDG_RUNTIME_DIR/agentspace/virtie-fake/virtiofs.sock"; test -f "$XDG_RUNTIME_DIR/agentspace/virtie-fake/virtiofs.sock.pid"; test -S "$XDG_RUNTIME_DIR/agentspace/virtie-fake/qmp.sock"; test -f overlay.img; test ! -e virtiofs.sock; test ! -e virtiofs.sock.pid; test ! -e qmp.sock; echo AGENTSPACE_VIRTIE_OK' >"$launch_log" 2>&1; then
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
