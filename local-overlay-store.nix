{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.agentspace.sandbox;
  overlayCfg = cfg.localOverlayStore;
  lowerStoreDir = "/nix/.ro-store";
  lowerStoreViewDir = "/nix/.local-overlay-lower-store";
  lowerStateDir = "/nix/var/nix";
  upperRoot = "/nix/.rw-store";
  upperLayer = "${upperRoot}/store";
  upperStateDir = "${upperRoot}/state";
  lowerStoreUri = "local?real=${lowerStoreViewDir}&state=${lowerStateDir}&read-only=true";
  remountHook = pkgs.writeShellScript "local-overlay-store-remount" ''
    set -eu

    # util-linux's new mount API cannot remount OverlayFS on affected kernels.
    export LIBMOUNT_FORCE_MOUNT2=always
    exec ${pkgs.util-linux}/bin/mount -o remount "$1"
  '';
  storeUri =
    "local-overlay://?lower-store=${lib.escapeURL lowerStoreUri}"
    + "&upper-layer=${lib.escapeURL upperLayer}"
    + "&state=${lib.escapeURL upperStateDir}"
    + "&remount-hook=${lib.escapeURL (toString remountHook)}"
    # The initrd mounts the overlay below /sysroot. After switch-root the
    # mount is correct, but /proc/self/mounts retains those old option paths.
    + "&check-mount=false";
  nixDaemonEnvironment = pkgs.writeText "local-overlay-store-environment" ''
    NIX_REMOTE=${storeUri}
  '';
  roStoreDisk =
    if config.microvm.storeDiskType == "erofs" then "/dev/disk/by-label/nix-store" else "/dev/vda";
in
{
  options.agentspace.sandbox.localOverlayStore = {
    enable = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Use Nix's experimental local-overlay store backend for the writable
        guest store instead of microvm.writableStoreOverlay's metadata model.
      '';
    };

    storeUri = lib.mkOption {
      type = lib.types.str;
      readOnly = true;
      internal = true;
      description = "Resolved local-overlay store URI used by nix-daemon.";
    };
  };

  config = lib.mkIf (cfg.enable && overlayCfg.enable) {
    assertions = [
      {
        assertion = config.nix.enable;
        message = "agentspace.sandbox.localOverlayStore requires Nix in the guest.";
      }
      {
        assertion = (config.nix.package.pname or null) == "nix";
        message = "agentspace.sandbox.localOverlayStore requires upstream Nix.";
      }
      {
        assertion = config.microvm.registerClosure;
        message = ''
          agentspace.sandbox.localOverlayStore requires microvm.registerClosure
          so the ephemeral lower-store database describes the boot closure.
        '';
      }
    ];

    agentspace.sandbox.localOverlayStore.storeUri = storeUri;

    # Replace microvm.nix's writableStoreOverlay wiring while retaining its
    # volume and read-only host-store mounts.
    microvm.writableStoreOverlay = lib.mkForce null;
    boot.initrd.kernelModules = [ "overlay" ];

    fileSystems = {
      "${upperRoot}".neededForBoot = true;

      "${lowerStoreDir}" = lib.mkIf config.microvm.storeOnDisk {
        device = roStoreDisk;
        fsType = config.microvm.storeDiskType;
        options = [
          "ro"
          "x-systemd.after=systemd-modules-load.service"
        ];
        neededForBoot = true;
        noCheck = true;
      };

      # TODO: Remove this view when read-only LocalStore stops creating .links.
      # microvm store images contain only store paths, while LocalStore creates
      # realStoreDir/.links even in read-only mode. Give that metadata reader a
      # private writable view without changing the immutable OverlayFS lowerdir.
      "${lowerStoreViewDir}" = {
        neededForBoot = true;
        overlay = {
          lowerdir = [ lowerStoreDir ];
          upperdir = "${upperRoot}/lower-store-view";
          workdir = "${upperRoot}/lower-store-view-work";
        };
      };

      "/nix/store" = lib.mkForce {
        neededForBoot = true;
        overlay = {
          lowerdir = [ lowerStoreDir ];
          upperdir = upperLayer;
          workdir = "${upperRoot}/work";
        };
      };
    };

    nix.settings.experimental-features = [
      "local-overlay-store"
      "read-only-local-store"
    ];

    # Clients continue to use the daemon socket. Only nix-daemon opens the
    # local-overlay store directly, preserving multi-user store permissions.
    systemd.services.nix-daemon = {
      enable = true;
      after = [ "post-boot.service" ];
      # Keep the percent-encoded URI out of Environment=, where systemd would
      # interpret percent escapes as unit specifiers.
      serviceConfig.EnvironmentFile = nixDaemonEnvironment;
      unitConfig.RequiresMountsFor = [
        "/nix/store"
        lowerStoreViewDir
        upperStateDir
      ];
    };
    systemd.sockets.nix-daemon.enable = true;
  };
}
