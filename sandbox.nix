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

    workspace = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Whether to mount the host current directory into the VM.";
      };

      mountPoint = lib.mkOption {
        type = lib.types.str;
        default = "/home/${cfg.user}/workspace";
        description = "Where to mount the workspace inside the VM.";
      };
    };

    persistence = {
      homeImage = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = "home.img";
        description = "Path to the ext4 disk image for home directory persistence. Set to null to disable.";
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
    ]
    ++ lib.optionals cfg.workspace.enable [
      "d ${cfg.workspace.mountPoint} 0755 ${cfg.user} users -"
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
      ++ lib.optionals cfg.workspace.enable [
        {
          proto = cfg.protocol;
          tag = "workspace";
          source = ".";
          mountPoint = cfg.workspace.mountPoint;
          securityModel = "mapped";
        }
      ];

      writableStoreOverlay = "/nix/.rw-store";

      volumes = [
        {
          image = cfg.persistence.storeOverlay;
          mountPoint = "/nix/.rw-store";
          size = 2048;
        }
      ]
      ++ lib.optionals (cfg.persistence.homeImage != null) [
        {
          image = cfg.persistence.homeImage;
          mountPoint = "/home/${cfg.user}";
          fsType = "ext4";
          size = 1024;
          autoCreate = true;
        }
      ];
    };
  };
}
