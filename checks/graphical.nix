{
  mkLaunch,
  mkSandbox,
  pkgs,
  ...
}:
let
  graphicalPrivateKey = pkgs.writeText "agentspace-graphical-test-key" ''
    -----BEGIN OPENSSH PRIVATE KEY-----
    b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
    QyNTUxOQAAACDs5JSkHdhirv4IlJL748zDDZ8ALUx+pK52d0sD1s2neQAAAKDFGLshxRi7
    IQAAAAtzc2gtZWQyNTUxOQAAACDs5JSkHdhirv4IlJL748zDDZ8ALUx+pK52d0sD1s2neQ
    AAAEBIFmjS+iJuRr/KCw7dOZpUHHWV8isoRjOO0dU2QQjQN+zklKQd2GKu/giUkvvjzMMN
    nwAtTH6krnZ3SwPWzad5AAAAGWFnZW50c3BhY2UtZ3JhcGhpY2FsLXRlc3QBAgME
    -----END OPENSSH PRIVATE KEY-----
  '';
  graphicalPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOzklKQd2GKu/giUkvvjzMMNnwAtTH6krnZ3SwPWzad5 agentspace-graphical-test";

  graphicalVM = mkSandbox {
    ssh.authorizedKeys = [ graphicalPublicKey ];
    ssh.identityFile = "./id_ed25519";
    persistence = {
      homeImage = null;
      storeOverlay = "nix-store-overlay.img";
    };
    mountWorkspace = false;
    machine = {
      memory = 768;
      vcpu = 1;
    };
    extraModules = [
      (
        { lib, ... }:
        {
          microvm.cpu = "max";
          microvm.graphics.enable = true;
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
          boot.kernelParams = lib.mkAfter [ "nomodeset=0" ];
        }
      )
    ];
  };

  launchScript = mkLaunch graphicalVM;
  manifest = graphicalVM.config.agentspace.sandbox.launch.virtieManifestData;
in
{
  graphical-manifest-contract =
    assert graphicalVM.config.microvm.graphics.enable == true;
    assert manifest.qemu.knobs.noGraphic == false;
    assert manifest.qemu.graphics.backend == graphicalVM.config.microvm.graphics.backend;
    assert builtins.elem "drm" graphicalVM.config.boot.kernelModules;
    assert builtins.elem "virtio_gpu" graphicalVM.config.boot.kernelModules;
    pkgs.runCommand "graphical-manifest-contract" { } "touch $out";

  graphical-real-boot-smoke = pkgs.runCommand "graphical-real-boot-smoke"
    {
      nativeBuildInputs = [
        pkgs.coreutils
        pkgs.openssh
        pkgs.xorg.xvfb
      ];
      __noChroot = true;
    }
    ''
      set -euo pipefail

      export HOME="$PWD/home"
      export XDG_RUNTIME_DIR="$PWD/xdg-runtime"
      export LIBGL_ALWAYS_SOFTWARE=1
      mkdir -p "$HOME" "$XDG_RUNTIME_DIR"
      chmod 700 "$XDG_RUNTIME_DIR"

      install -m 0600 ${graphicalPrivateKey} ./id_ed25519

      xvfb-run -a -s "-screen 0 1024x768x24" \
        timeout 240s ${launchScript} \
          bash -lc 'test -d /sys/class/drm && ls /sys/class/drm/card* >/dev/null && command -v run-wayland-proxy >/dev/null'

      touch "$out"
    '';
}
