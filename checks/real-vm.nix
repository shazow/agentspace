{
  mkLaunch,
  mkSandbox,
  mkExecSSH,
  pkgs,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };
  testKey = sshKeys.graphical;

  realVM = mkSandbox {
    hostName = "rv";
    quiet = false;
    ssh.authorizedKeys = [ testKey.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = "./id_ed25519";
    };
    persistence = {
      baseDir = ".agentspace";
      homeImage = null;
      storeDisk = true;
    };
    machine.memory = 512;
    workspace = {
      enable = true;
      hostDir = ".";
      addCurrentDir = false;
    };
  };

  launchScript = mkLaunch realVM;

  realVMSmokeDriver = pkgs.writeShellApplication {
    name = "consumer-real-vm-smoke-driver";
    runtimeInputs = [
      pkgs.coreutils
      pkgs.gnugrep
    ];
    text = ''
      set -euo pipefail

      out="$1"
      if [ ! -e /dev/vhost-vsock ]; then
        echo "consumer-real-vm-smoke: /dev/vhost-vsock is not visible in the Nix sandbox" >&2
        echo "consumer-real-vm-smoke: add /dev/vhost-vsock to nix.settings.extra-sandbox-paths" >&2
        exit 1
      fi

      if [ -n "''${WORKSPACE:-}" ] && mkdir -p "$WORKSPACE/tmp" 2>/dev/null; then
        scratch_parent="$WORKSPACE/tmp"
      else
        scratch_parent="''${NIX_BUILD_TOP:-$PWD}/tmp"
        mkdir -p "$scratch_parent"
      fi

      base_dir="$(mktemp -d "$scratch_parent/rv.XXXXXX")"
      launch_dir="$base_dir/w"
      log="$base_dir/launch.log"

      finish() {
        status=$?
        if [ "$status" -ne 0 ]; then
          echo "consumer-real-vm-smoke: failed with status $status" >&2
          if [ -f "$log" ]; then
            echo "== $log ==" >&2
            cat "$log" >&2
          fi
        else
          rm -rf "$base_dir"
        fi
      }
      trap finish EXIT

      mkdir -p "$launch_dir" "$base_dir/home" "$base_dir/runtime"
      chmod 700 "$base_dir/home" "$base_dir/runtime"

      export HOME="$base_dir/home"
      export XDG_RUNTIME_DIR="$base_dir/runtime"

      cd "$launch_dir"
      printf 'real vm smoke\n' > sentinel
      install -m 0600 ${testKey.privateKey} ./id_ed25519

      # The remote command is intentionally single-quoted so it expands inside the guest.
      # shellcheck disable=SC2016
      timeout 180s ${launchScript} bash -lc '
        set -euo pipefail
        test -f "$WORKSPACE/sentinel"
        grep -F "real vm smoke" "$WORKSPACE/sentinel" >/dev/null
        echo AGENTSPACE_REAL_VM_OK
      ' >"$log" 2>&1

      mkdir -p "$out"
      cp "$log" "$out/launch.log"
      grep -F AGENTSPACE_REAL_VM_OK "$out/launch.log" >/dev/null
    '';
  };
in
{
  consumer-real-vm-smoke =
    pkgs.runCommandLocal "consumer-real-vm-smoke"
      {
        allowSubstitutes = false;
        preferLocalBuild = true;
        requiredSystemFeatures = [ "kvm" ];
      }
      ''
        ${realVMSmokeDriver}/bin/consumer-real-vm-smoke-driver "$out"
      '';
}
