{
  mkLaunch,
  mkSandbox,
  pkgs,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };

  graphicalVM = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.graphical.publicKey ];
    ssh.identityFile = sshKeys.graphical.identityFile;
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
          microvm.virtiofsd.group = null;
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
    assert manifest.graphics.backend == graphicalVM.config.microvm.graphics.backend;
    assert builtins.elem "drm" graphicalVM.config.boot.kernelModules;
    assert builtins.elem "virtio_gpu" graphicalVM.config.boot.kernelModules;
    pkgs.runCommand "graphical-manifest-contract" { } "touch $out";

  graphical-real-boot-smoke = pkgs.runCommand "graphical-real-boot-smoke"
    {
      nativeBuildInputs = [
        pkgs.coreutils
        pkgs.openssh
        pkgs.xvfb-run
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

      install -m 0600 ${sshKeys.graphical.privateKey} ./id_ed25519

      xvfb-run -a -s "-screen 0 1024x768x24" \
        timeout 240s ${launchScript} \
          bash -lc 'test -d /sys/class/drm && ls /sys/class/drm/card* >/dev/null && command -v run-wayland-proxy >/dev/null'

      touch "$out"
    '';
}
