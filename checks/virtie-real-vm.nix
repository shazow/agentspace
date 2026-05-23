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
  # End-to-end virtie suspend/resume coverage without nesting an extra NixOS
  # test VM: the test driver launches a real mkSandbox guest through the
  # generated virtie manifest, starts stateful guest work over SSH, lets the guest
  # initiate `systemctl suspend`, then resumes through `virtie launch
  # --resume=auto`. It asserts the saved suspend metadata (`status`, `runState`,
  # and `source`), the vmstate/suspend file lifecycle, preserved /run session
  # state, and that the same guest process is still present and making progress
  # after resume.
  virtie-real-guest-suspend-retains-session-state =
    (pkgs.testers.runNixOSTest {
      name = "virtie-real-guest-suspend-retains-session-state";
      nodes = { };

      testScript = # python
      ''
        import json
        import os
        import pathlib
        import subprocess
        import textwrap

        workspace = pathlib.Path(os.environ.get("TMPDIR", "/build")) / "virtie-real"
        state_dir = workspace / ".agentspace"
        manifest_path = pathlib.Path("${manifestPath}")
        vmstate_path = workspace / ".agentspace/real-suspend.vmstate"
        suspend_state_path = workspace / ".agentspace/real-suspend.suspend.json"
        virtie = "${virtiePackage}/bin/virtie"

        env = os.environ.copy()
        env["HOME"] = str(workspace / "home")
        env["XDG_RUNTIME_DIR"] = str(workspace / "run")
        env["VIRTIE_SSH_READY_TIMEOUT"] = "10m"

        start_process_and_suspend = textwrap.dedent(r"""
            set -euo pipefail

            sudo tee /run/virtie-process.sh >/dev/null <<'SH'
        #!/bin/sh
        i=0
        while :; do
          i=$((i + 1))
          printf "%s\n" "$i" > /run/virtie-process-counter
          sleep 1
        done
        SH
            sudo chmod 0755 /run/virtie-process.sh
            sudo sh -c 'nohup /run/virtie-process.sh >/run/virtie-process.log 2>&1 & echo $! > /run/virtie-process.pid'

            for _ in $(seq 1 20); do
              test -s /run/virtie-process-counter && break
              sleep 0.25
            done
            test -s /run/virtie-process-counter
            test -d "/proc/$(cat /run/virtie-process.pid)"
            printf "%s\n" "state-before-suspend" | sudo tee /run/virtie-session-state >/dev/null
            sudo systemctl suspend
        """)

        verify_resumed_process = textwrap.dedent(r"""
            set -euo pipefail

            test "$(cat /run/virtie-session-state)" = state-before-suspend
            pid="$(cat /run/virtie-process.pid)"
            test -d "/proc/$pid"
            tr "\0" " " <"/proc/$pid/cmdline" | grep -F "/run/virtie-process.sh"
            before="$(cat /run/virtie-process-counter)"
            sleep 2
            after="$(cat /run/virtie-process-counter)"
            test "$after" -gt "$before"
        """)

        def run(argv: list[str], log_name: str | None = None, timeout: int = 900) -> None:
            result = subprocess.run(
                argv,
                cwd=workspace,
                env=env,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                timeout=timeout,
            )
            if log_name is not None:
                (workspace / log_name).write_text(result.stdout, encoding="utf-8")
            if result.returncode != 0:
                print(result.stdout)
                raise Exception(f"command failed with exit code {result.returncode}: {argv!r}")

        def assert_suspend_state() -> None:
            if not vmstate_path.is_file():
                raise Exception(f"missing vmstate: {vmstate_path}")
            if not suspend_state_path.is_file():
                raise Exception(f"missing suspend state: {suspend_state_path}")

            state = json.loads(suspend_state_path.read_text(encoding="utf-8"))
            expected = {
                "status": "saved",
                "runState": "suspended",
                "source": "guest-suspend",
            }
            for key, value in expected.items():
                actual = state.get(key)
                if actual != value:
                    raise Exception(f"{key}: got {actual!r}, want {value!r}")

        with subtest("prepare launch workspace"):
            workspace.mkdir(parents=True, exist_ok=True)
            (workspace / "home").mkdir(parents=True, exist_ok=True)
            (workspace / "run").mkdir(parents=True, exist_ok=True)
            state_dir.mkdir(parents=True, exist_ok=True)
            run(["install", "-m", "0600", "${sshKeys.graphical.privateKey}", "id_ed25519"])
            run(["install", "-m", "0644", "${manifestTemplate}", str(manifest_path)])

        with subtest("launch guest, start process, and save suspend state"):
            run(
                [
                    virtie,
                    "launch",
                    "--ssh",
                    f"--manifest={manifest_path}",
                    "--",
                    "bash",
                    "-lc",
                    start_process_and_suspend,
                ],
                log_name="launch.log",
            )
            assert_suspend_state()

        with subtest("resume guest and verify session process survived"):
            run(
                [
                    virtie,
                    "launch",
                    "--ssh",
                    "--resume=auto",
                    f"--manifest={manifest_path}",
                    "--",
                    "bash",
                    "-lc",
                    verify_resumed_process,
                ],
                log_name="resume.log",
            )
            if vmstate_path.exists():
                raise Exception(f"vmstate was not removed after resume: {vmstate_path}")
            if suspend_state_path.exists():
                raise Exception(f"suspend state was not removed after resume: {suspend_state_path}")
      '';
    }).overrideTestDerivation
      (_: {
        __noChroot = true;
      });
}
