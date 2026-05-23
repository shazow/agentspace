{
  mkSandbox,
  mkExecSSH,
  pkgs,
  virtiePackage,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };

  guest = mkSandbox {
    hostName = "real-suspend";
    ssh.authorizedKeys = [ sshKeys.graphical.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = "./id_ed25519";
    };
    workspace.enable = false;
    persistence = {
      homeImage = "home.img";
      homeSize = 512;
      storeOverlay = "nix-store-overlay.img";
    };
    machine.memory = 768;
    extraModules = [
      (
        { ... }:
        {
          microvm.cpu = "max";
          microvm.qemu.machineOpts = {
            accel = "tcg";
            mem-merge = "on";
            acpi = "on";
            pit = "off";
            pic = "off";
            pcie = "on";
            rtc = "on";
            usb = "off";
          };
        }
      )
    ];
  };

  manifestTemplate = guest.config.agentspace.sandbox.launch.virtieManifestTemplate;
  manifestPath = guest.config.agentspace.sandbox.launch.virtieManifest;
in
{
  virtie-real-guest-suspend-retains-session-state =
    (pkgs.testers.runNixOSTest {
      name = "virtie-real-guest-suspend-retains-session-state";
      nodes = { };

      testScript = ''
                import os
                import pathlib
                import subprocess
                import textwrap
                import time

                workspace = pathlib.Path(os.environ.get("TMPDIR", "/build")) / "virtie-real"
                state_dir = workspace / ".agentspace"

                env = os.environ.copy()
                env["HOME"] = str(workspace / "home")
                env["XDG_RUNTIME_DIR"] = str(workspace / "run")
                env["VIRTIE_SSH_READY_TIMEOUT"] = "10m"

                def run_shell(command: str, timeout: int = 900) -> None:
                    result = subprocess.run(
                        ["bash", "-lc", textwrap.dedent(command)],
                        cwd=workspace,
                        env=env,
                        text=True,
                        stdout=subprocess.PIPE,
                        stderr=subprocess.STDOUT,
                        timeout=timeout,
                    )
                    if result.returncode != 0:
                        print(result.stdout)
                        raise Exception(f"command failed with exit code {result.returncode}")

                def wait_until_succeeds(command: str, timeout: int = 30) -> None:
                    deadline = time.monotonic() + timeout
                    last_output = ""
                    while time.monotonic() < deadline:
                        result = subprocess.run(
                            ["bash", "-lc", command],
                            cwd=workspace,
                            env=env,
                            text=True,
                            stdout=subprocess.PIPE,
                            stderr=subprocess.STDOUT,
                        )
                        if result.returncode == 0:
                            return
                        last_output = result.stdout
                        time.sleep(0.5)
                    print(last_output)
                    raise Exception(f"timed out waiting for command to succeed: {command}")

                with subtest("prepare launch workspace"):
                    workspace.mkdir(parents=True, exist_ok=True)
                    (workspace / "home").mkdir(parents=True, exist_ok=True)
                    (workspace / "run").mkdir(parents=True, exist_ok=True)
                    state_dir.mkdir(parents=True, exist_ok=True)
                    run_shell("""
                      install -m 0600 ${sshKeys.graphical.privateKey} id_ed25519
                      install -m 0644 ${manifestTemplate} ${manifestPath}
                    """)

                with subtest("launch guest, start process, and save suspend state"):
                    run_shell("""
                      if ! ${virtiePackage}/bin/virtie launch --ssh --manifest=${manifestPath} -- \
                        bash -lc '
                          set -euo pipefail
                          sudo tee /run/virtie-process.sh >/dev/null <<'"'"'SH'"'"'
        #!/bin/sh
        i=0
        while :; do
          i=$((i + 1))
          printf "%s\n" "$i" > /run/virtie-process-counter
          sleep 1
        done
        SH
                          sudo chmod 0755 /run/virtie-process.sh
                          sudo sh -c "nohup /run/virtie-process.sh >/run/virtie-process.log 2>&1 & echo \\$! > /run/virtie-process.pid"
                          for _ in $(seq 1 20); do
                            test -s /run/virtie-process-counter && break
                            sleep 0.25
                          done
                          test -s /run/virtie-process-counter
                          test -d "/proc/$(cat /run/virtie-process.pid)"
                          printf "%s\n" "state-before-suspend" | sudo tee /run/virtie-session-state >/dev/null
                          sudo systemctl suspend
                        ' \
                        >launch.log 2>&1; then
                        cat launch.log
                        exit 1
                      fi
                    """)
                    wait_until_succeeds("test -f .agentspace/real-suspend.vmstate")
                    wait_until_succeeds("test -f .agentspace/real-suspend.suspend.json")
                    run_shell("""
                      grep -F '"status": "saved"' .agentspace/real-suspend.suspend.json
                      grep -F '"runState": "suspended"' .agentspace/real-suspend.suspend.json
                      grep -F '"source": "guest-suspend"' .agentspace/real-suspend.suspend.json
                    """)

                with subtest("resume guest and verify session process survived"):
                    run_shell("""
                      if ! ${virtiePackage}/bin/virtie launch --ssh --resume=auto --manifest=${manifestPath} -- \
                        bash -lc '
                          set -euo pipefail
                          test "$(cat /run/virtie-session-state)" = state-before-suspend
                          pid="$(cat /run/virtie-process.pid)"
                          test -d "/proc/$pid"
                          tr "\0" " " <"/proc/$pid/cmdline" | grep -F "/run/virtie-process.sh"
                          before="$(cat /run/virtie-process-counter)"
                          sleep 2
                          after="$(cat /run/virtie-process-counter)"
                          test "$after" -gt "$before"
                        ' \
                        >resume.log 2>&1; then
                        cat resume.log
                        exit 1
                      fi
                    """)
                    wait_until_succeeds("test ! -e .agentspace/real-suspend.vmstate")
                    wait_until_succeeds("test ! -e .agentspace/real-suspend.suspend.json")
      '';
    }).overrideTestDerivation
      (_: {
        __noChroot = true;
      });
}
