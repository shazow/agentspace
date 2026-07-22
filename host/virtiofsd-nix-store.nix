# Host-side NixOS module: a socket-activated virtiofsd sharing the host's
# read-only /nix/store. Point `agentspace.sandbox.nixStoreShareSocket` at the
# resulting socket path to reuse this daemon across launches instead of
# having virtle start its own for the ro-store share.
#
# Adapted from https://github.com/shazow/nixfiles/blob/main/modules/virtiofsd-nix-store.nix
{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.agentspace.hostVirtiofsdNixStore;
in
{
  options.agentspace.hostVirtiofsdNixStore = {
    enable = lib.mkEnableOption "socket-activated virtiofsd sharing the host's read-only /nix/store";

    socketGroup = lib.mkOption {
      type = lib.types.str;
      default = "kvm";
      description = "Group owning the virtiofsd socket.";
    };

    ownHardening = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Disable virtiofsd's own sandboxing and rely on systemd hardening instead.";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.sockets."virtiofs-nix-store" = {
      description = "Socket for virtiofsd /nix/store read-only share";
      wantedBy = [ "sockets.target" ];
      socketConfig = {
        ListenStream = "/run/virtiofs-nix-store.sock";
        SocketGroup = cfg.socketGroup;
        SocketMode = "0660";
      };
    };

    systemd.services."virtiofs-nix-store" = {
      description = "virtiofsd for /nix/store";
      requires = [ "virtiofs-nix-store.socket" ];
      after = [ "virtiofs-nix-store.socket" ];

      serviceConfig = lib.mkMerge [
        {
          Type = "simple";
          User = "root";
          Group = "root";
          LimitNOFILE = 1048576;

          ExecStart = pkgs.writeShellScript "virtiofsd-wrapper" ''
            exec ${pkgs.virtiofsd}/bin/virtiofsd \
              --fd=3 \
              --shared-dir=/nix/store \
              --thread-pool-size $(${pkgs.coreutils}/bin/nproc) \
              --posix-acl \
              --xattr \
              --cache=auto \
              --inode-file-handles=mandatory \
              --sandbox=${if cfg.ownHardening then "none" else "namespace"} \
              --readonly
          '';
        }
        (lib.mkIf cfg.ownHardening {
          # systemd-native hardening, used in place of virtiofsd's own sandbox
          # when ownHardening is enabled.
          PrivateDevices = true;
          PrivateNetwork = true;
          PrivateTmp = true;
          ProtectControlGroups = true;
          ProtectHome = true;
          ProtectKernelTunables = true;
          ProtectSystem = "strict";
          ReadOnlyPaths = [ "/nix/store" ];

          RestrictAddressFamilies = [ "AF_UNIX" ];
          ProtectHostname = true;
          ProtectClock = true;
          ProtectKernelModules = true;
          ProtectKernelLogs = true;
          NoNewPrivileges = true;
          RestrictRealtime = true;
          RestrictSUIDSGID = true;
        })
      ];
    };
  };
}
