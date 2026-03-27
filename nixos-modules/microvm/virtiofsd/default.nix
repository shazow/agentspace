{
  config,
  lib,
  pkgs,
  ...
}:

let
  virtiofsShares = builtins.filter ({ proto, ... }: proto == "virtiofs") config.microvm.shares;

  requiresVirtiofsd = virtiofsShares != [ ] && config.microvm.hypervisor != "vfkit";

  systemdSocketActivate = lib.getExe' config.microvm.vmHostPackages.systemd "systemd-socket-activate";

  mkVirtiofsdCommand =
    {
      tag,
      source,
      readOnly,
      ...
    }:
    pkgs.writeShellScript "virtiofsd-${tag}" ''
      if [ "$(id -u)" = 0 ]; then
        opt_rlimit=(--rlimit-nofile 1048576)
      else
        opt_rlimit=()
      fi

      exec ${lib.getExe config.microvm.virtiofsd.package} \
        --fd=3 \
        --shared-dir=${lib.escapeShellArg source} \
        "''${opt_rlimit[@]}" \
        --thread-pool-size ${toString config.microvm.virtiofsd.threadPoolSize} \
        --posix-acl --xattr \
        ${
          lib.optionalString (
            config.microvm.virtiofsd.inodeFileHandles != null
          ) "--inode-file-handles=${config.microvm.virtiofsd.inodeFileHandles}"
        } \
        ${lib.optionalString (config.microvm.hypervisor == "crosvm") "--tag=${tag}"} \
        ${lib.optionalString readOnly "--readonly"} \
        ${lib.escapeShellArgs config.microvm.virtiofsd.extraArgs}
    '';

  mkLaunch =
    {
      tag,
      socket,
      ...
    }@share:
    ''
      socket=${lib.escapeShellArg socket}
      rm -f "$socket"
      ${systemdSocketActivate} \
        --listen="$socket" \
        --fdname=${lib.escapeShellArg "virtiofsd-${tag}"} \
        ${mkVirtiofsdCommand share} &
      pids+=("$!")
    '';

in
{
  microvm.binScripts = lib.mkIf requiresVirtiofsd {
    virtiofsd-run = lib.mkForce ''
      set -euo pipefail

      pids=()
      cleanup() {
        for pid in "''${pids[@]}"; do
          if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
          fi
        done
      }

      trap cleanup EXIT INT TERM

      ${lib.concatMapStringsSep "\n" mkLaunch virtiofsShares}

      # FIXME: Intentionally no per-share socket readiness loop.
      # This keeps startup minimal and relies on systemd-socket-activate owning
      # socket creation before QEMU connects.
      #
      # FIXME: We currently do not post-adjust socket group/mode here. That means
      # microvm.virtiofsd.group may not be applied for activator-owned sockets.
      # Revisit if we need stronger ownership guarantees.

      if [ "${builtins.toString (builtins.length virtiofsShares)}" = 0 ]; then
        exit 0
      fi

      set +e
      wait -n "''${pids[@]}"
      status=$?
      set -e

      if [ "$status" -ne 0 ]; then
        echo "virtiofsd-run: an activator exited unexpectedly" >&2
      else
        echo "virtiofsd-run: an activator exited early" >&2
        status=1
      fi

      exit "$status"
    '';
  };
}
