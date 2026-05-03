{
  mkSandbox,
  pkgs,
  ...
}:
let
  alpinePublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGyJmKbRoN7faJz3tUdmuy6EDj0bvmJQO0dW1Ob0pUtM agentspace-alpine-e2e";
  alpinePrivateKey = pkgs.writeText "agentspace-alpine-e2e-key" ''
    -----BEGIN OPENSSH PRIVATE KEY-----
    b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
    QyNTUxOQAAACBsiZim0aDe32ic97VHZrsuhA49G75iUDtHVtTm9KVLTAAAAJhn1LqgZ9S6
    oAAAAAtzc2gtZWQyNTUxOQAAACBsiZim0aDe32ic97VHZrsuhA49G75iUDtHVtTm9KVLTA
    AAAEC0X5l3KEssq706th7k0ptmhIYqjoFzwgDVMpOlcgDcDGyJmKbRoN7faJz3tUdmuy6E
    Dj0bvmJQO0dW1Ob0pUtMAAAAFWFnZW50c3BhY2UtYWxwaW5lLWUyZQ==
    -----END OPENSSH PRIVATE KEY-----
  '';

  tcgMachineOpts = {
    accel = "tcg";
    acpi = "on";
    mem-merge = "on";
    pic = "off";
    pcie = "on";
    pit = "off";
    rtc = "on";
    usb = "off";
  };

  vmAlpine = mkSandbox {
    alpine.enable = true;
    hostName = "agent-sandbox-alpine-e2e";
    mountWorkspace = false;
    ssh = {
      autoconnect = false;
      authorizedKeys = [ alpinePublicKey ];
    };
    persistence.basedir = ".agentspace-alpine-e2e";
    extraModules = [
      {
        microvm = {
          # The check forces TCG so it can run in the normal Nix build sandbox
          # without /dev/kvm. A non-null CPU also keeps the generated manifest
          # from enabling KVM or using host CPU passthrough.
          cpu = "max";
          mem = 256;
          vcpu = 1;
          qemu.machineOpts = tcgMachineOpts;
        };
      }
    ];
  };

  launchCfg = vmAlpine.config.agentspace.sandbox.launch;
  qemu = launchCfg.virtieManifestData.qemu;
  hostName = vmAlpine.config.agentspace.sandbox.hostName;
in
{
  sandbox-alpine-e2e = pkgs.runCommand "sandbox-alpine-e2e" { } ''
    set -euo pipefail

    tmpdir="$(mktemp -d)"
    root_img="$tmpdir/alpine-root.img"
    qmp_socket="$tmpdir/qmp.sock"
    serial_log="$tmpdir/alpine-serial.log"
    ssh_key="$tmpdir/id_ed25519"

    cleanup() {
      status=$?
      if [ "$status" -ne 0 ]; then
        echo "sandbox-alpine-e2e: failed with status $status" >&2
        if [ -f "$serial_log" ]; then
          echo "== $serial_log ==" >&2
          cat "$serial_log" >&2
        fi
      fi
      if [ -n "''${qemu_pid:-}" ]; then
        kill "$qemu_pid" 2>/dev/null || true
        wait "$qemu_pid" 2>/dev/null || true
      fi
      rm -rf "$tmpdir"
    }
    trap cleanup EXIT INT TERM

    cp ${launchCfg.alpineRootDisk}/alpine-root.img "$root_img"
    chmod u+w "$root_img"
    install -m 0600 ${alpinePrivateKey} "$ssh_key"

    ${qemu.binaryPath} \
      -name ${hostName} \
      -M q35,accel=tcg \
      -m 256 \
      -smp 1 \
      -nodefaults \
      -no-user-config \
      -no-reboot \
      -nographic \
      -kernel ${qemu.kernel.path} \
      -initrd ${qemu.kernel.initrdPath} \
      -append '${qemu.kernel.params}' \
      -serial "file:$serial_log" \
      -qmp "unix:$qmp_socket,server,nowait" \
      -drive "id=vda,format=raw,file=$root_img,if=none,cache=unsafe" \
      -device virtio-blk-pci,drive=vda,serial=agentspace-alpine-root \
      -netdev user,id=microvm1,hostfwd=tcp:127.0.0.1:10022-:22 \
      -device virtio-net-pci,netdev=microvm1,mac=02:02:00:00:00:01 \
      >"$tmpdir/qemu.log" 2>&1 &
    qemu_pid=$!

    for _ in $(seq 1 120); do
      if grep -F 'Starting sshd ... [ ok ]' "$serial_log" >/dev/null 2>&1; then
        break
      fi
      sleep 0.5
    done
    grep -F 'Starting sshd ... [ ok ]' "$serial_log" >/dev/null

    ssh_status=1
    for _ in $(seq 1 120); do
      if ${pkgs.openssh}/bin/ssh \
        -q \
        -o BatchMode=yes \
        -o ConnectTimeout=1 \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o GlobalKnownHostsFile=/dev/null \
        -i "$ssh_key" \
        -p 10022 \
        agent@127.0.0.1 \
        'test -f /etc/alpine-release && test -d /home/agent && grep -qx agent-sandbox-alpine-e2e /etc/hostname && grep -q agentspace-alpine-e2e /home/agent/.ssh/authorized_keys'
      then
        ssh_status=0
        break
      fi
      sleep 0.5
    done
    if [ "$ssh_status" -ne 0 ]; then
      echo "sandbox-alpine-e2e: ssh probe failed" >&2
      exit 1
    fi

    ${pkgs.python3}/bin/python - "$qmp_socket" <<'PY'
    import json
    import socket
    import sys

    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
        sock.settimeout(5)
        sock.connect(sys.argv[1])
        sock.recv(4096)
        for command in ("qmp_capabilities", "quit"):
            sock.sendall(json.dumps({"execute": command}).encode("utf-8") + b"\r\n")
            sock.recv(4096)
    PY

    if ! wait "$qemu_pid"; then
      echo "sandbox-alpine-e2e: qemu exited non-zero" >&2
      exit 1
    fi
    unset qemu_pid

    mkdir -p "$out"
    cp "$serial_log" "$out/alpine-serial.log"
  '';
}
