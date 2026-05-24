{
  mkSandbox,
  mkExecSSH,
  pkgs,
  virtiePackage,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };

  # This should be a very simple test:
  # 1. Boot a basic minimal VM
  # 2. Run `sleep 12345 &` and save the $PID in the test runner via $! output
  # 3. Run `sudo systemctl suspend`
  # 4. Use `virtie launch --resume=force --manifest=$MANIFEST` to resume the VM
  # 5. Confirm that $PID still exists and the command is "sleep 12345"
  mkGuestSuspendCheck =
    { name }:
    pkgs.testers.runNixOSTest {
      inherit name;
      nodes = { };

      testScript = # python
        ''
          import os
          import pathlib
          import subprocess
          import textwrap

          workspace = pathlib.Path(os.environ.get("TMPDIR", "/build")) / "${name}"
          state_dir = workspace / ".agentspace"
          manifest_path = pathlib.Path("${manifestPath}")
          virtie = "${virtiePackage}/bin/virtie"

          env = os.environ.copy()
          env["HOME"] = str(workspace / "home")
          env["XDG_RUNTIME_DIR"] = str(workspace / "run")
          env["VIRTIE_SSH_READY_TIMEOUT"] = "10m"

          start_sleep_and_suspend = textwrap.dedent(r"""
              set -euo pipefail

              trap "" HUP
              sleep 12345 &
              sleep_pid="$!"
              test -d "/proc/$sleep_pid"
              printf "VIRTIE_SLEEP_PID=%s\n" "$sleep_pid"
              sudo systemctl suspend
              while :; do
                sleep 60
              done
          """)

          verify_resumed_process = textwrap.dedent(r"""
              set -euo pipefail

              pid="$1"
              test -d "/proc/$pid"
              cmdline="$(tr "\0" " " <"/proc/$pid/cmdline")"
              test "$cmdline" = "sleep 12345 "
          """)

          def run(argv: list[str], log_name: str | None = None, timeout: int = 900, check: bool = True) -> subprocess.CompletedProcess[str]:
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
              if check and result.returncode != 0:
                  print(result.stdout)
                  raise Exception(f"command failed with exit code {result.returncode}: {argv!r}")
              return result

          def parse_sleep_pid(output: str) -> str:
              for line in output.splitlines():
                  if line.startswith("VIRTIE_SLEEP_PID="):
                      pid = line.removeprefix("VIRTIE_SLEEP_PID=")
                      if pid.isdecimal() and int(pid) > 0:
                          return pid
                      raise Exception(f"invalid sleep pid: {pid!r}")
              raise Exception("launch output did not include VIRTIE_SLEEP_PID")

          with subtest("prepare launch workspace"):
              workspace.mkdir(parents=True, exist_ok=True)
              (workspace / "home").mkdir(parents=True, exist_ok=True)
              (workspace / "run").mkdir(parents=True, exist_ok=True)
              state_dir.mkdir(parents=True, exist_ok=True)
              run(["install", "-m", "0600", "${sshKeys.graphical.privateKey}", "id_ed25519"])
              run(["install", "-m", "0644", "${manifestTemplate}", str(manifest_path)])

          with subtest("launch guest, start sleep command, and suspend"):
              launch = run(
                  [
                      virtie,
                      "launch",
                      "--ssh",
                      f"--manifest={manifest_path}",
                      "--",
                      "bash",
                      "-lc",
                      start_sleep_and_suspend,
                  ],
                  log_name="launch.log",
              )
              sleep_pid = parse_sleep_pid(launch.stdout)

          with subtest("resume guest and verify session process survived"):
              run(
                  [
                      virtie,
                      "launch",
                      "--ssh",
                      "--resume=force",
                      f"--manifest={manifest_path}",
                      "--",
                      "bash",
                      "-lc",
                      verify_resumed_process,
                      "verify-resumed-process",
                      sleep_pid,
                  ],
                  log_name="resume.log",
              )
        '';
    };

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
      storeDisk = true;
    };
    machine.memory = 768;
    # Keep a serial log available for debugging failed VM boots. The test uses
    # virtie's normal QEMU/KVM/vsock path, so sandboxed Nix builds need
    # /dev/kvm and /dev/vhost-vsock exposed by the daemon.
    extraModules = [
      (
        { lib, pkgs, ... }:
        {
          boot.consoleLogLevel = lib.mkForce 7;
          boot.initrd.verbose = true;
          boot.initrd.kernelModules = [ "vmw_vsock_virtio_transport" ];
          services.logind.settings.Login.SleepOperation = "suspend";
          # FIXME: We can get rid of this when we fix https://github.com/shazow/agentspace/issues/118
          # and use qemu.quiet = false;
          microvm.qemu.extraArgs = [
            "-serial"
            "file:console.log"
          ];
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
  # generated virtie manifest, starts a process over SSH, asks systemd to
  # suspend the guest, then resumes through `virtie launch --resume=force` and
  # verifies the same guest process is still present.
  #
  # Validated on 2026-05-24 with normal sandbox settings:
  # `time -p nix build --no-link .#legacyPackages.x86_64-linux.realVMChecks.virtie-real-guest-suspend-retains-session-state`
  # Runtime after this refactor: 38.14s wall clock.
  virtie-real-guest-suspend-retains-session-state = mkGuestSuspendCheck {
    name = "virtie-real-guest-suspend-retains-session-state";
  };
}
