{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.agentspace.sandbox;
in
{
  options.agentspace.sandbox = {
    enable = lib.mkEnableOption "Agent Sandbox MicroVM Environment";

    user = lib.mkOption {
      type = lib.types.str;
      default = "agent";
      description = "Username for the guest.";
    };

    hostName = lib.mkOption {
      type = lib.types.str;
      default = "agent-sandbox";
      description = "Hostname for the guest VM.";
    };

    protocol = lib.mkOption {
      type = lib.types.enum [
        "9p"
        "virtiofs"
      ];
      default = "9p";
      description = "File share protocol. '9p' runs in userland (slow), 'virtiofs' requires root (fast).";
    };

    inbox = lib.mkOption {
      type = lib.types.listOf (
        lib.types.submodule {
          options = {
            source = lib.mkOption {
              type = lib.types.str;
              description = "Host-side path (relative to agent dir at runtime).";
            };
            mountPoint = lib.mkOption {
              type = lib.types.str;
              description = "Where to mount the share inside the VM.";
            };
          };
        }
      );
      default = [
        {
          source = "inbox/repo";
          mountPoint = "/home/${cfg.user}/mnt/inbox/repo";
        }
      ];
      description = "Read-only shares mounted into the VM.";
    };

    outbox = {
      mountPoint = lib.mkOption {
        type = lib.types.str;
        default = "/home/${cfg.user}/mnt/outbox";
        description = "Where to mount the writable outbox inside the VM.";
      };
    };

    persistence = {
      homeImage = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = "home.img";
        description = "Path to the ext4 disk image for home directory persistence. Set to null to disable.";
      };

      homeSize = lib.mkOption {
        type = lib.types.number;
        default = 4096;
        description = "Size of persistent home image in MB.";
      };

      storeOverlay = lib.mkOption {
        type = lib.types.str;
        default = "nix-store-overlay.img";
        description = "Path for the writable nix store overlay image.";
      };
    };

    bundle = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "List of file paths to bundle into the VM runtime";
    };
  };

  config = lib.mkIf cfg.enable {
    networking.hostName = cfg.hostName;
    system.stateVersion = lib.trivial.release;
    nixpkgs.config.allowUnfree = true;

    # Boot & Kernel
    boot.kernel.sysctl."kernel.unprivileged_userns_clone" = 1;
    boot.kernelParams = [
      "quiet"
      "udev.log_level=3"
    ];
    boot.consoleLogLevel = 0;
    boot.initrd.verbose = false;

    # User Configuration
    users.users.${cfg.user} = {
      password = "";
      isNormalUser = true;
      extraGroups = [ "wheel" ];
    };

    security.sudo.wheelNeedsPassword = false;
    services.getty.autologinUser = cfg.user;

    # Directory permissions
    systemd.tmpfiles.rules = [
      "d /home/${cfg.user} 0700 ${cfg.user} users -"
      "f /home/${cfg.user}/.bash_logout 0600 ${cfg.user} users - sudo poweroff"
    ];

    # Basic Package Set
    environment.systemPackages = with pkgs; [
      bashInteractive
      coreutils
      curl
      fd
      git
      gnugrep
      less
      neovim
      which
    ];

    # MicroVM Configuration
    microvm = {
      mem = 4 * 1024;
      balloon = true;
      socket = "/tmp/vm-${cfg.hostName}.sock";
      hypervisor = "qemu";

      qemu.extraArgs = [
        "-cpu"
        "host"
      ];

      interfaces = [
        {
          type = "user";
          id = "microvm1";
          mac = "02:02:00:00:00:01";
        }
      ];

      shares = [
        {
          proto = cfg.protocol;
          tag = "ro-store";
          source = "/nix/store";
          mountPoint = "/nix/.ro-store";
        }
      ]
      ++ lib.imap0 (i: inbox: {
        proto = cfg.protocol;
        tag = "inbox-${toString i}";
        source = inbox.source;
        mountPoint = inbox.mountPoint;
        readOnly = true;
      }) cfg.inbox
      ++ [
        {
          proto = cfg.protocol;
          tag = "outbox";
          source = "outbox";
          mountPoint = cfg.outbox.mountPoint;
          securityModel = "mapped";
        }
      ];

      writableStoreOverlay = "/nix/.rw-store";

      volumes = [
        {
          image = cfg.persistence.storeOverlay;
          mountPoint = "/nix/.rw-store";
          size = 4096;
        }
      ]
      ++ lib.optionals (cfg.persistence.homeImage != null) [
        {
          image = cfg.persistence.homeImage;
          mountPoint = "/home/${cfg.user}";
          fsType = "ext4";
          size = cfg.persistence.homeSize;
          autoCreate = true;
        }
      ];
    };
  };
}
