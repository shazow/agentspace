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
  # End-to-end virtie guest suspend/resume coverage without nesting an extra
  # NixOS test VM: the test driver launches a real mkSandbox guest through the
  # generated virtie manifest, starts a process over SSH, asks the guest to
  # suspend itself, then resumes through `virtie launch --resume=force` and
  # verifies the same guest process is still present.
  #
  # Validated on 2026-05-24 with normal sandbox settings:
  # `time -p nix build --no-link .#legacyPackages.x86_64-linux.realVMChecks.virtie-real-guest-suspend-retains-session-state`
  # Runtime: 152.37s wall clock.
  virtie-real-guest-suspend-retains-session-state =
    (pkgs.testers.runNixOSTest {
      name = "virtie-real-guest-suspend-retains-session-state";
      nodes = { };

      testScript = # python
        ''
          import os
          import pathlib
          import select
          import subprocess
          import textwrap
          import time

          workspace = pathlib.Path(os.environ.get("TMPDIR", "/build")) / "virtie-real"
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
              sudo systemd-inhibit --what=sleep --why=virtie-test sleep 10 &
              inhibit_pid="$!"
              sudo systemctl suspend || test "$?" = 1
              wait "$inhibit_pid"
              printf "VIRTIE_SYSTEMCTL_SUSPEND_RETURNED\n"
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

          def run(argv: list[str], log_name: str | None = None, timeout: int = 900) -> str:
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
              return result.stdout

          def parse_sleep_pid(output: str) -> str:
              for line in output.splitlines():
                  if line.startswith("VIRTIE_SLEEP_PID="):
                      pid = line.removeprefix("VIRTIE_SLEEP_PID=")
                      if pid.isdecimal() and int(pid) > 0:
                          return pid
                      raise Exception(f"invalid sleep pid: {pid!r}")
              raise Exception("launch output did not include VIRTIE_SLEEP_PID")

          def read_until(process: subprocess.Popen, log_file, timeout: int) -> tuple[str, str]:
              deadline = time.monotonic() + timeout
              output = []
              sleep_pid = None
              saw_suspend_return = False

              stdout = process.stdout
              if stdout is None:
                  raise Exception("launch process stdout was not captured")

              while time.monotonic() < deadline:
                  ready, _, _ = select.select([stdout], [], [], 1)
                  if not ready:
                      if process.poll() is not None:
                          raise Exception(f"launch exited before suspend was requested: {process.returncode}")
                      continue

                  line = stdout.readline()
                  if line == "":
                      if process.poll() is not None:
                          raise Exception(f"launch exited before suspend was requested: {process.returncode}")
                      continue
                  output.append(line)
                  log_file.write(line)
                  log_file.flush()

                  if line.startswith("VIRTIE_SLEEP_PID="):
                      sleep_pid = parse_sleep_pid(line)
                  if "VIRTIE_SYSTEMCTL_SUSPEND_RETURNED" in line:
                      saw_suspend_return = True
                  if sleep_pid is not None and saw_suspend_return:
                      return sleep_pid, "".join(output)

              raise TimeoutError("timed out waiting for guest suspend command")

          def finish_launch(process: subprocess.Popen, log_file, timeout: int = 300) -> None:
              try:
                  output, _ = process.communicate(timeout=timeout)
              except subprocess.TimeoutExpired:
                  process.terminate()
                  try:
                      output, _ = process.communicate(timeout=30)
                  except subprocess.TimeoutExpired:
                      process.kill()
                      output, _ = process.communicate()
                  log_file.write(output)
                  raise

              log_file.write(output)
              if process.returncode != 0:
                  raise Exception(f"launch failed with exit code {process.returncode}")

          with subtest("prepare launch workspace"):
              workspace.mkdir(parents=True, exist_ok=True)
              (workspace / "home").mkdir(parents=True, exist_ok=True)
              (workspace / "run").mkdir(parents=True, exist_ok=True)
              state_dir.mkdir(parents=True, exist_ok=True)
              run(["install", "-m", "0600", "${sshKeys.graphical.privateKey}", "id_ed25519"])
              run(["install", "-m", "0644", "${manifestTemplate}", str(manifest_path)])

          with subtest("launch guest, start sleep, and save guest suspend state"):
              launch_log = (workspace / "launch.log").open("w", encoding="utf-8")
              launch = subprocess.Popen(
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
                  cwd=workspace,
                  env=env,
                  text=True,
                  stdout=subprocess.PIPE,
                  stderr=subprocess.STDOUT,
              )
              try:
                  sleep_pid, _ = read_until(launch, launch_log, 90)
                  run([virtie, "suspend", f"--manifest={manifest_path}"], log_name="suspend.log", timeout=300)
                  finish_launch(launch, launch_log)
              finally:
                  if launch.poll() is None:
                      launch.terminate()
                      try:
                          launch.wait(timeout=30)
                      except subprocess.TimeoutExpired:
                          launch.kill()
                  launch_log.close()

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
    });
}
