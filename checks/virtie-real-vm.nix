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
    # This check runs a guest VM from inside another VM. In this environment the
    # normal mkSandbox defaults try the KVM path (`accel=kvm:tcg`, host CPU) and
    # QEMU reaches QMP but the guest never reports SSH readiness. Forcing an
    # explicit CPU makes virtie avoid `-enable-kvm`, and forcing `accel=tcg`
    # keeps QEMU out of nested KVM entirely. The serial log confirmed the image
    # was also failing before userspace when `pit=off`: Linux had no PIT/HPET/PM
    # timer source for early TSC calibration under TCG. The remaining machine
    # options mirror microvm's x86 defaults for the devices this test uses:
    # `pcie=on` is needed for the PCI virtio devices, `acpi=on`/`rtc=on` keep
    # platform plumbing available, `pit=on` keeps early boot timers available,
    # `pic=off`/`usb=off` keep the microvm device model minimal, and
    # `mem-merge=on` preserves the normal memory behavior.
    extraModules = [
      (
        { lib, pkgs, ... }:
        {
          boot.consoleLogLevel = lib.mkForce 7;
          boot.initrd.verbose = true;
          boot.kernelParams = lib.mkAfter [
            "console=ttyS0"
            "earlyprintk=ttyS0"
            "udev.log_level=7"
          ];
          boot.initrd.kernelModules = [ "vmw_vsock_virtio_transport" ];
          microvm.cpu = "max";
          microvm.qemu.extraArgs = [
            "-serial"
            "file:console.log"
          ];
          systemd.services.virtie-ssh-signal.serviceConfig.ExecStartPre =
            "${pkgs.coreutils}/bin/sleep 5";
          microvm.qemu.machineOpts = {
            accel = "tcg";
            mem-merge = "on";
            acpi = "on";
            pit = "on";
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
  # generated virtie manifest, starts stateful guest work over SSH, saves the live
  # VM through `virtie suspend`, then resumes through `virtie launch
  # --resume=auto`. It asserts the saved suspend metadata (`status`, `runState`,
  # and `source`), the vmstate/suspend file lifecycle, preserved /run session
  # state, and that the same guest process is still present and making progress
  # after resume.
  #
  # Validated on 2026-05-23 with:
  # `time -p sudo nix build --no-link --option sandbox false .#legacyPackages.x86_64-linux.realVMChecks.virtie-real-guest-suspend-retains-session-state`
  # Runtime: 54.71s wall clock.
  virtie-real-guest-suspend-retains-session-state =
    (pkgs.testers.runNixOSTest {
      name = "virtie-real-guest-suspend-retains-session-state";
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

          start_process_and_wait = textwrap.dedent(r"""
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
              printf "%s\n" "VIRTIE_READY_FOR_HOST_SUSPEND"
              while :; do
                sleep 60
              done
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

          def wait_for_output(process: subprocess.Popen, log_file, needle: str, timeout: int) -> None:
              deadline = time.monotonic() + timeout
              stdout = process.stdout
              if stdout is None:
                  raise Exception("launch process stdout was not captured")

              while time.monotonic() < deadline:
                  ready, _, _ = select.select([stdout], [], [], 1)
                  if not ready:
                      if process.poll() is not None:
                          raise Exception(f"launch exited before {needle!r}: {process.returncode}")
                      continue

                  line = stdout.readline()
                  if line == "":
                      if process.poll() is not None:
                          raise Exception(f"launch exited before {needle!r}: {process.returncode}")
                      continue
                  log_file.write(line)
                  log_file.flush()
                  if needle in line:
                      return

              raise TimeoutError(f"timed out waiting for {needle!r}")

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

          def assert_suspend_state() -> None:
              if not vmstate_path.is_file():
                  raise Exception(f"missing vmstate: {vmstate_path}")
              if not suspend_state_path.is_file():
                  raise Exception(f"missing suspend state: {suspend_state_path}")

              state = json.loads(suspend_state_path.read_text(encoding="utf-8"))
              expected = {
                  "status": "saved",
                  "runState": "running",
                  "source": "virtie",
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
                      start_process_and_wait,
                  ],
                  cwd=workspace,
                  env=env,
                  text=True,
                  stdout=subprocess.PIPE,
                  stderr=subprocess.STDOUT,
              )
              try:
                  wait_for_output(launch, launch_log, "VIRTIE_READY_FOR_HOST_SUSPEND", 900)
                  run([virtie, "suspend", f"--manifest={manifest_path}"], log_name="suspend.log", timeout=300)
                  finish_launch(launch, launch_log)
                  assert_suspend_state()
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
