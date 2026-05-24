{
  mkSandbox,
  mkExecSSH,
  pkgs,
  virtiePackage,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };

  mkGuestSuspendCheck =
    {
      name,
      sleepCommand,
    }:
    pkgs.testers.runNixOSTest {
      inherit name;
      nodes = { };

      testScript = # python
        ''
          import json
          import os
          import pathlib
          import select
          import subprocess
          import textwrap
          import time

          workspace = pathlib.Path(os.environ.get("TMPDIR", "/build")) / "${name}"
          state_dir = workspace / ".agentspace"
          manifest_path = pathlib.Path("${manifestPath}")
          suspend_state_path = state_dir / "real-suspend.suspend.json"
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
              set +e
              ${sleepCommand}
              sleep_status="$?"
              set -e
              printf "VIRTIE_SLEEP_COMMAND_STATUS=%s\n" "$sleep_status"
              test "$sleep_status" = 0 -o "$sleep_status" = 1
              wait "$inhibit_pid" || true
              printf "VIRTIE_INHIBIT_DONE\n"
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

          def read_sleep_pid(process: subprocess.Popen, log_file, timeout: int) -> str:
              deadline = time.monotonic() + timeout
              output = []

              stdout = process.stdout
              if stdout is None:
                  raise Exception("launch process stdout was not captured")

              while time.monotonic() < deadline:
                  ready, _, _ = select.select([stdout], [], [], 1)
                  if not ready:
                      if process.poll() is not None:
                          raise Exception(f"launch exited before reporting sleep pid: {process.returncode}")
                      continue

                  line = stdout.readline()
                  if line == "":
                      if process.poll() is not None:
                          raise Exception(f"launch exited before reporting sleep pid: {process.returncode}")
                      continue
                  output.append(line)
                  log_file.write(line)
                  log_file.flush()

                  if line.startswith("VIRTIE_SLEEP_PID="):
                      return parse_sleep_pid("".join(output))

              raise TimeoutError("timed out waiting for guest sleep pid")

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

          def parse_sleep_command_status(line: str) -> int:
              status = line.removeprefix("VIRTIE_SLEEP_COMMAND_STATUS=").strip()
              if status.isdecimal():
                  return int(status)
              raise Exception(f"invalid sleep command status: {status!r}")

          def wait_for_suspend_signal(process: subprocess.Popen, log_file, timeout: int) -> str:
              deadline = time.monotonic() + timeout
              output = []
              stdout = process.stdout
              if stdout is None:
                  raise Exception("launch process stdout was not captured")

              while time.monotonic() < deadline:
                  ready, _, _ = select.select([stdout], [], [], 1)
                  if ready:
                      line = stdout.readline()
                      if line != "":
                          output.append(line)
                          log_file.write(line)
                          log_file.flush()
                          if line.startswith("VIRTIE_SLEEP_COMMAND_STATUS="):
                              status = parse_sleep_command_status(line)
                              if status not in (0, 1):
                                  raise Exception(f"sleep command failed with exit code {status}")
                          if "VIRTIE_INHIBIT_DONE" in line:
                              return "command-returned"

                  if suspend_state_path.exists():
                      assert_suspend_state(run_state="suspended", source="guest-suspend")
                      finish_launch(process, log_file)
                      return "guest-suspend"

                  if process.poll() is not None:
                      remaining, _ = process.communicate(timeout=1)
                      output.append(remaining)
                      log_file.write(remaining)
                      log_file.flush()
                      print("".join(output))
                      if suspend_state_path.exists():
                          assert_suspend_state(run_state="suspended", source="guest-suspend")
                          if process.returncode != 0:
                              raise Exception(f"launch failed with exit code {process.returncode}")
                          return "guest-suspend"
                      if process.returncode != 0:
                          raise Exception(f"launch failed with exit code {process.returncode}")
                      raise Exception("launch exited before saving guest suspend state")

              print("".join(output))
              raise TimeoutError("timed out waiting for guest suspend signal")

          def assert_suspend_state(run_state: str, source: str) -> None:
              with suspend_state_path.open("r", encoding="utf-8") as state_file:
                  state = json.load(state_file)

              if state.get("status") != "saved":
                  raise Exception(f"unexpected suspend status: {state!r}")
              if state.get("runState") != run_state:
                  raise Exception(f"unexpected suspend runState: {state!r}")
              if state.get("source") != source:
                  raise Exception(f"unexpected suspend source: {state!r}")
              if not isinstance(state.get("cid"), int) or state["cid"] <= 0:
                  raise Exception(f"invalid suspend cid: {state!r}")

              vm_state_path = pathlib.Path(state.get("vmStatePath", ""))
              if not vm_state_path.is_file():
                  raise Exception(f"saved vm state path is missing: {state!r}")

          with subtest("prepare launch workspace"):
              workspace.mkdir(parents=True, exist_ok=True)
              (workspace / "home").mkdir(parents=True, exist_ok=True)
              (workspace / "run").mkdir(parents=True, exist_ok=True)
              state_dir.mkdir(parents=True, exist_ok=True)
              run(["install", "-m", "0600", "${sshKeys.graphical.privateKey}", "id_ed25519"])
              run(["install", "-m", "0644", "${manifestTemplate}", str(manifest_path)])

          with subtest("launch guest, start sleep command, and save suspend state"):
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
                  sleep_pid = read_sleep_pid(launch, launch_log, 90)
                  suspend_signal = wait_for_suspend_signal(launch, launch_log, 120)
                  if suspend_signal == "command-returned":
                      run([virtie, "suspend", f"--manifest={manifest_path}"], log_name="suspend.log", timeout=300)
                      finish_launch(launch, launch_log)
                      assert_suspend_state(run_state="running", source="virtie")
              finally:
                  if launch.poll() is None:
                      launch.terminate()
                      try:
                          launch.wait(timeout=30)
                      except subprocess.TimeoutExpired:
                          launch.kill()
                  launch_log.close()

          with subtest("virtie suspend reports no active launch after guest suspend"):
              if suspend_signal == "guest-suspend":
                  suspend = run(
                      [virtie, "suspend", f"--manifest={manifest_path}"],
                      log_name="suspend.log",
                      timeout=300,
                      check=False,
                  )
                  if suspend.returncode == 0:
                      raise Exception("virtie suspend unexpectedly succeeded after guest suspend already saved state")
                  if "does not exist" not in suspend.stdout and "is virtie launch running" not in suspend.stdout:
                      print(suspend.stdout)
                      raise Exception("virtie suspend did not report a missing active launch pid")

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
  # generated virtie manifest, starts a process over SSH, asks systemd to enter
  # a sleep path, saves suspend state once the guest command has either returned
  # or produced a QMP guest-suspend state, then resumes through
  # `virtie launch --resume=force` and verifies the same guest process is still
  # present.
  #
  # Validated on 2026-05-24 with normal sandbox settings:
  # `time -p nix build --no-link .#legacyPackages.x86_64-linux.realVMChecks.virtie-real-guest-suspend-retains-session-state`
  # Runtime after this refactor: 38.14s wall clock.
  virtie-real-guest-suspend-retains-session-state = mkGuestSuspendCheck {
    name = "virtie-real-guest-suspend-retains-session-state";
    sleepCommand = "sudo systemctl suspend";
  };

  virtie-real-guest-systemctl-sleep-retains-session-state = mkGuestSuspendCheck {
    name = "virtie-real-guest-systemctl-sleep-retains-session-state";
    sleepCommand = "sudo systemctl sleep";
  };
}
