{
  mkLaunch,
  mkSandbox,
  pkgs,
  virtiePackage,
  ...
}:
let
  hotplugVM = mkSandbox {
    quiet = false;
    ssh = {
      autoconnect = false;
      exec = [ ];
    };
    # Sandboxed QEMU cannot create host vsock sockets, and this check only
    # needs QMP plus the guest-agent serial channel.
    vsock.enable = false;
    persistence = {
      homeImage = null;
      # Use a disk-backed Nix store because sandboxed QEMU cannot share the host
      # store through virtiofs during this hotplug validation.
      storeDisk = true;
    };
    workspace.enable = false;
    machine = {
      memory = 512;
      vcpu = 1;
    };
    hotplug.mounts = [
      {
        tag = "cache";
        source = ".agentspace-test/cache";
        target = "/mnt/cache";
      }
    ];
    extraModules = [
      (
        { ... }:
        {
          # Q35 provides PCIe root ports for virtiofs hotplug. The microvm
          # machine does not leave enough PCI bridge resources for this topology.
          microvm.qemu.machine = "q35";
          microvm.qemu.machineOpts = {
            accel = "kvm:tcg";
          };
          microvm.virtiofsd.group = null;
        }
      )
    ];
  };

  launchScript = mkLaunch hotplugVM;
  manifestPath = hotplugVM.config.agentspace.sandbox.launch.virtieManifest;
  systemClosure = hotplugVM.config.system.build.toplevel;
  systemClosureInfo = pkgs.closureInfo { rootPaths = [ systemClosure ]; };
  qgaExec = pkgs.writeTextFile {
    name = "virtie-qga-exec";
    destination = "/bin/virtie-qga-exec";
    executable = true;
    text = ''
      #!${pkgs.python3}/bin/python3
      import base64
      import json
      import socket
      import sys
      import time

      sock_path = sys.argv[1]
      argv = sys.argv[2:]
      deadline = time.monotonic() + 120

      def decode(data):
          if not data:
              return ""
          return base64.b64decode(data).decode("utf-8", "replace")

      def connect():
          while True:
              try:
                  conn = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                  conn.settimeout(1)
                  conn.connect(sock_path)
                  return conn
              except OSError:
                  if time.monotonic() > deadline:
                      raise
                  time.sleep(0.1)

      conn = connect()
      read_buffer = b""

      def run(execute, arguments=None):
          global read_buffer
          payload = {"execute": execute}
          if arguments is not None:
              payload["arguments"] = arguments
          conn.sendall(json.dumps(payload).encode() + b"\r\n")
          while True:
              if time.monotonic() > deadline:
                  raise TimeoutError(f"guest agent command {execute} did not return")
              while b"\n" not in read_buffer:
                  try:
                      chunk = conn.recv(4096)
                  except TimeoutError:
                      if time.monotonic() > deadline:
                          raise TimeoutError(f"guest agent command {execute} did not return")
                      continue
                  if not chunk:
                      raise RuntimeError("guest agent connection closed")
                  read_buffer += chunk
              line, read_buffer = read_buffer.split(b"\n", 1)
              if not line:
                  continue
              message = json.loads(line)
              if "event" in message:
                  continue
              if "error" in message:
                  raise RuntimeError(message["error"])
              return message.get("return")

      run("guest-ping")
      if not argv:
          conn.close()
          sys.exit(0)
      result = run("guest-exec", {"path": argv[0], "arg": argv[1:], "capture-output": True})
      pid = result["pid"]
      while True:
          status = run("guest-exec-status", {"pid": pid})
          if status.get("exited"):
              sys.stdout.write(decode(status.get("out-data", "")))
              sys.stderr.write(decode(status.get("err-data", "")))
              conn.close()
              sys.exit(status.get("exitcode", 1))
          if time.monotonic() > deadline:
              raise TimeoutError(f"guest command pid {pid} did not exit")
          time.sleep(0.1)
    '';
  };
  qmpReady = pkgs.writeTextFile {
    name = "virtie-qmp-ready";
    destination = "/bin/virtie-qmp-ready";
    executable = true;
    text = ''
      #!${pkgs.python3}/bin/python3
      import json
      import socket
      import sys
      import time

      sock_path = sys.argv[1]
      deadline = time.monotonic() + 120

      def recv_message(conn):
          data = b""
          while b"\n" not in data:
              if time.monotonic() > deadline:
                  raise TimeoutError("qmp did not respond")
              chunk = conn.recv(4096)
              if not chunk:
                  raise RuntimeError("qmp connection closed")
              data += chunk
          return json.loads(data.split(b"\n", 1)[0])

      conn = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
      conn.settimeout(1)
      conn.connect(sock_path)
      greeting = recv_message(conn)
      if "QMP" not in greeting:
          raise RuntimeError(f"unexpected qmp greeting: {greeting!r}")
      conn.sendall(json.dumps({"execute": "qmp_capabilities"}).encode() + b"\r\n")
      response = recv_message(conn)
      conn.close()
      if "error" in response:
          raise RuntimeError(response["error"])
    '';
  };
in
{
  virtie-hotplug-real-vm =
    pkgs.runCommand "virtie-hotplug-real-vm"
      {
        nativeBuildInputs = [
          pkgs.coreutils
          pkgs.gnugrep
        ];
        requiredSystemFeatures = [ "kvm" ];
      }
      ''
        set -euo pipefail

        tmpdir="$(mktemp -d "''${TMPDIR:-/tmp}/virtie-hotplug-vm.XXXXXX")"
        workspace_dir="$tmpdir/workspace"
        launch_log="$tmpdir/virtie-launch.log"
        hotplug_log="$tmpdir/virtie-hotplug.log"
        failed=0
        trap 'echo "virtie-hotplug-real-vm: command failed at line $LINENO" >&2' ERR

        cleanup() {
          status=$?
          if [ "$status" -ne 0 ] && [ "$failed" -eq 0 ]; then
            failed=1
            echo "virtie-hotplug-real-vm: failed with status $status" >&2
            for log in "$launch_log" "$hotplug_log"; do
              if [ -f "$log" ]; then
                echo "== $log ==" >&2
                cat "$log" >&2
              fi
            done
            if [ -f "$workspace_dir/${manifestPath}" ]; then
              echo "== manifest ==" >&2
              cat "$workspace_dir/${manifestPath}" >&2
            fi
          fi
          if [ -n "''${hotplug_pid:-}" ]; then
            kill "$hotplug_pid" 2>/dev/null || true
            wait "$hotplug_pid" 2>/dev/null || true
          fi
          if [ -n "''${launch_pid:-}" ]; then
            kill "$launch_pid" 2>/dev/null || true
            wait "$launch_pid" 2>/dev/null || true
          fi
          rm -rf "$tmpdir"
        }
        trap cleanup EXIT INT TERM

        export HOME="$tmpdir/home"
        export XDG_RUNTIME_DIR="$tmpdir/run"
        export VIRTIE_SSH_READY_TIMEOUT=5m
        mkdir -p "$HOME" "$XDG_RUNTIME_DIR" "$workspace_dir/.agentspace-test/cache"
        chmod 700 "$XDG_RUNTIME_DIR"
        test -x ${systemClosure}/prepare-root
        test -f ${systemClosureInfo}/registration

        printf '%s\n' "host payload" > "$workspace_dir/.agentspace-test/cache/payload"
        cd "$workspace_dir"

        qga_exec() {
          ${qgaExec}/bin/virtie-qga-exec "$workspace_dir/.agentspace/qga.sock" "$@"
        }

        ${launchScript} >"$launch_log" 2>&1 &
        launch_pid=$!

        manifest="$workspace_dir/${manifestPath}"
        for _ in $(seq 1 600); do
          if [ -S "$workspace_dir/.agentspace/qmp.sock" ] \
            && [ -S "$workspace_dir/.agentspace/qga.sock" ] \
            && [ -f "$manifest" ] \
            && qga_exec >/dev/null 2>&1 \
            && ${qmpReady}/bin/virtie-qmp-ready "$workspace_dir/.agentspace/qmp.sock" >/dev/null 2>&1; then
            break
          fi
          if ! kill -0 "$launch_pid" 2>/dev/null; then
            wait "$launch_pid"
          fi
          sleep 0.1
        done
        test -S "$workspace_dir/.agentspace/qmp.sock"
        test -S "$workspace_dir/.agentspace/qga.sock"
        test -f "$manifest"
        qga_exec >/dev/null
        ${qmpReady}/bin/virtie-qmp-ready "$workspace_dir/.agentspace/qmp.sock" >/dev/null

        ${virtiePackage}/bin/virtie --manifest="$manifest" -v hotplug cache >"$hotplug_log" 2>&1 &
        hotplug_pid=$!

        for _ in $(seq 1 600); do
          if [ -f "$workspace_dir/.agentspace/hotplug/cache.json" ]; then
            break
          fi
          if ! kill -0 "$hotplug_pid" 2>/dev/null; then
            wait "$hotplug_pid"
          fi
          sleep 0.1
        done
        test -f "$workspace_dir/.agentspace/hotplug/cache.json"

        qga_exec /run/current-system/sw/bin/sh -c '
          set -eu
          for _ in $(seq 1 300); do
            if [ -f /mnt/cache/payload ]; then
              cat /mnt/cache/payload > /mnt/cache/guest-read
              printf "%s\n" "guest wrote through hotplug" > /mnt/cache/guest-write
              exit 0
            fi
            sleep 0.1
          done
          echo "hotplug payload did not appear" >&2
          /run/current-system/sw/bin/mount | /run/current-system/sw/bin/grep -F /mnt/cache >&2 || true
          /run/current-system/sw/bin/ls -la /mnt/cache >&2 || true
          /run/current-system/sw/bin/dmesg | /run/current-system/sw/bin/tail -n 80 >&2 || true
          exit 1
        '

        grep -Fx "host payload" "$workspace_dir/.agentspace-test/cache/guest-read" >/dev/null
        grep -Fx "guest wrote through hotplug" "$workspace_dir/.agentspace-test/cache/guest-write" >/dev/null

        kill "$hotplug_pid"
        wait "$hotplug_pid" 2>/dev/null || true
        unset hotplug_pid
        test ! -e "$workspace_dir/.agentspace/hotplug/cache.json"

        kill "$launch_pid"
        wait "$launch_pid" 2>/dev/null || true
        unset launch_pid

        mkdir -p "$out"
        cp "$launch_log" "$out/virtie-launch.log"
        cp "$hotplug_log" "$out/virtie-hotplug.log"
      '';
}
