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
      default = "virtiofs";
      description = "File share protocol. '9p' runs in userland (slow), 'virtiofs' requires a daemon (fast).";
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

    mountWorkspace = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Mount the current working directory into the VM as the workspace share.";
    };

    swapSize = lib.mkOption {
      type = lib.types.int;
      default = 0;
      description = "Size of the guest sparse swapfile in MiB. Set to 0 to disable (e.g. 4096).";
    };

    connectWith = lib.mkOption {
      type = lib.types.enum [
        "console"
        "ssh"
      ];
      default = "ssh";
      description = "Auto-connect via serial console or ssh. When using ssh, make sure to set sshAuthorizedKeys.";
    };

    sshAuthorizedKeys = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Setup ssh access with these authorized keys.";
    };

    sshIdentityFile = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = "Optional SSH identity file passed to host-side launch/connect helpers.";
    };

    workspaceMountPoint = lib.mkOption {
      type = lib.types.str;
      default = "/home/${cfg.user}/workspace";
      description = "Where to mount the current working directory inside the VM.";
    };

    homeModules = lib.mkOption {
      type = lib.types.listOf lib.types.raw;
      default = [ ];
      description = "Extra Home Manager modules imported for the sandbox user.";
    };

    extraModules = lib.mkOption {
      type = lib.types.listOf lib.types.raw;
      default = [ ];
      description = "Extra modules imported for NixOS.";
    };

    initExtra = lib.mkOption {
      type = lib.types.separatedString "\n";
      description = "Extra shell snippet appended to the launch-agent script.";
      default = ''
        echo "🚀 Preparing Agent QEMU Sandbox..."
      ''
      + lib.optionalString cfg.mountWorkspace ''
        echo "📂 Mounting current directory at ~/workspace"
        cd "$REPO_DIR"
      '';
    };

    launch = {
      commonInit = lib.mkOption {
        type = lib.types.lines;
        readOnly = true;
        internal = true;
      };

      virtieManifest = lib.mkOption {
        type = lib.types.path;
        readOnly = true;
        internal = true;
      };
    };
  };

  config = lib.mkIf cfg.enable (
    let
      agentspace-bundle = pkgs.writeShellScriptBin "agentspace-bundle" (
        builtins.readFile ./scripts/agentspace-bundle
      );
      agentspace-init = pkgs.writeShellScriptBin "agentspace-init" (
        builtins.readFile ./scripts/agentspace-init
      );
      agentspace-logout = pkgs.writeShellScriptBin "agentspace-logout" ''
        set -eu

        uptime_seconds="$(cut -d. -f1 /proc/uptime)"
        if [ "$uptime_seconds" -gt 0 ] && fd --type f --hidden --follow --changed-within "''${uptime_seconds}s" . "$HOME" | {
          read -r _
        }; then
          printf '%s' "Found files in \$HOME modified since boot. Power off anyway? [N/y] "
          read -r confirm
          case "$confirm" in
            y|Y) ;;
            *) exit 0 ;;
          esac
        fi

        exec sudo poweroff
      '';

      initDirs =
        lib.unique (builtins.map (volume: builtins.dirOf volume.image) config.microvm.volumes);

      defaultInitExtra =
        ''
          echo "🚀 Preparing Agent QEMU Sandbox..."
        ''
        + lib.optionalString cfg.mountWorkspace ''
          echo "📂 Mounting current directory at ~/workspace"
          cd "$REPO_DIR"
        '';

      airlockEnabled = lib.attrByPath [ "airlock" "enable" ] false cfg;
      sshBaseArgv =
        [
          "${pkgs.openssh}/bin/ssh"
          "-q"
          "-o"
          "StrictHostKeyChecking=no"
          "-o"
          "UserKnownHostsFile=/dev/null"
          "-o"
          "GlobalKnownHostsFile=/dev/null"
        ]
        ++ lib.optionals (cfg.sshIdentityFile != null) [
          "-i"
          cfg.sshIdentityFile
        ];
      virtiofsShares = builtins.filter (share: share.proto == "virtiofs") config.microvm.shares;
      qemuArgvTemplate = import ./agentspace-qemu-argv.nix {
        inherit config lib pkgs;
      };

      mkVirtioFSDaemonCommand =
        {
          tag,
          socket,
          source,
          readOnly,
          cache,
          ...
        }:
        pkgs.writeShellScript "virtiofsd-${cfg.hostName}-${tag}" ''
          if [ "$(id -u)" = 0 ]; then
            opt_rlimit=(--rlimit-nofile 1048576)
          else
            opt_rlimit=()
          fi

          exec ${lib.getExe config.microvm.virtiofsd.package} \
            --socket-path=${lib.escapeShellArg socket} \
            ${lib.optionalString (config.microvm.virtiofsd.group != null)
              "--socket-group=${config.microvm.virtiofsd.group}"
            } \
            --shared-dir=${lib.escapeShellArg source} \
            "''${opt_rlimit[@]}" \
            --thread-pool-size ${toString config.microvm.virtiofsd.threadPoolSize} \
            --posix-acl --xattr \
            --cache=${cache} \
            ${
              lib.optionalString (
                config.microvm.virtiofsd.inodeFileHandles != null
              ) "--inode-file-handles=${config.microvm.virtiofsd.inodeFileHandles}"
            } \
            ${lib.optionalString (config.microvm.hypervisor == "crosvm") "--tag=${tag}"} \
            ${lib.optionalString readOnly "--readonly"} \
            ${lib.escapeShellArgs config.microvm.virtiofsd.extraArgs}
        '';

      virtiofsDaemons = builtins.map (share: {
        tag = share.tag;
        socketPath = share.socket;
        command = {
          path = mkVirtioFSDaemonCommand share;
          args = [ ];
        };
      }) virtiofsShares;

      manifestVolumes = builtins.map (volume: {
        imagePath = volume.image;
        sizeMiB = volume.size;
        fsType = volume.fsType;
        autoCreate = volume.autoCreate;
        label = volume.label;
        mkfsExtraArgs = volume.mkfsExtraArgs;
      }) config.microvm.volumes;

      virtieManifest = pkgs.writeText "virtie-${cfg.hostName}.json" (
        builtins.toJSON {
          identity.hostName = cfg.hostName;
          paths = {
            workingDir = ".";
            lockPath = "/tmp/agentspace-${cfg.hostName}.lock";
          };
          persistence.directories = initDirs;
          ssh = {
            argv = sshBaseArgv;
            user = cfg.user;
          };
          qemu.argvTemplate = qemuArgvTemplate;
          volumes = manifestVolumes;
          virtiofs.daemons = virtiofsDaemons;
        }
      );
    in
    lib.mkMerge [
      {
        assertions = [
          {
            assertion = cfg.protocol == "virtiofs";
            message = "agentspace.sandbox.protocol=9p is unsupported; only virtiofs is supported by the virtie-only launcher.";
          }
          {
            assertion = cfg.connectWith == "ssh";
            message = "agentspace.sandbox.connectWith=console is unsupported; only ssh is supported by the virtie-only launcher.";
          }
          {
            assertion = !airlockEnabled;
            message = "agentspace.sandbox.airlock.enable is unsupported; airlock launch behavior has been removed until it is reimplemented on top of virtie.";
          }
          {
            assertion = cfg.initExtra == defaultInitExtra;
            message = "Custom agentspace.sandbox.initExtra launch behavior is unsupported; only the default virtie prelaunch shell is supported right now.";
          }
          {
            assertion = config.microvm.hypervisor == "qemu";
            message = "Only microvm.hypervisor = \"qemu\" is supported by the dynamic virtie vsock launcher.";
          }
          {
            assertion = config.microvm.vsock.cid == null;
            message = "Static microvm.vsock.cid is unsupported; virtie allocates the vsock CID at launch time.";
          }
          {
            assertion = !config.microvm.registerWithMachined;
            message = "microvm.registerWithMachined is unsupported until dynamic vsock CID state is plumbed through machined registration.";
          }
          {
            assertion = lib.all (share: share.proto == "virtiofs") config.microvm.shares;
            message = "Only virtiofs shares are supported by the direct virtie QEMU launcher.";
          }
          {
            assertion = lib.all (interface: interface.type == "user") config.microvm.interfaces;
            message = "Only microvm.interfaces.*.type = \"user\" is supported by the direct virtie QEMU launcher.";
          }
          {
            assertion = config.microvm.graphics.enable == false;
            message = "microvm.graphics.enable is unsupported by the direct virtie QEMU launcher.";
          }
          {
            assertion = config.microvm.devices == [ ];
            message = "microvm.devices passthrough is unsupported by the direct virtie QEMU launcher.";
          }
          {
            assertion = config.microvm.storeOnDisk == false;
            message = "microvm.storeOnDisk is unsupported by the direct virtie QEMU launcher.";
          }
          {
            assertion = config.microvm.preStart == "";
            message = "microvm.preStart is unsupported by the direct virtie QEMU launcher.";
          }
          {
            assertion = config.microvm.extraArgsScript == null;
            message = "microvm.extraArgsScript is unsupported by the direct virtie QEMU launcher.";
          }
          {
            assertion = config.microvm.qemu.pcieRootPorts == [ ];
            message = "microvm.qemu.pcieRootPorts is unsupported by the direct virtie QEMU launcher.";
          }
        ];

        agentspace.sandbox.launch = {
          commonInit = cfg.initExtra;
          inherit virtieManifest;
        };

        networking.hostName = cfg.hostName;
        nixpkgs.config.allowUnfree = true;
        services.logrotate.enable = false;

        # Boot & Kernel
        boot.kernel.sysctl."kernel.unprivileged_userns_clone" = 1;
        boot.kernelParams = [
          "quiet"
          "udev.log_level=3"
        ];
        boot.consoleLogLevel = 0;
        boot.initrd.verbose = false;
        boot.tmp.useTmpfs = false;

        # Need this to fix running out of fd's, but requires root (or CAP_DAC_READ_SEARCH)
        #   microvm.virtiofsd.inodeFileHandles = "mandatory";
        # Workaround during runtime:
        # $ sync && echo 3 > /proc/sys/vm/drop_caches
        # Also:
        boot.kernel.sysctl = {
          # Increase likelihood guest will release inodes, but contingent on memory pressure
          # so unlikely to work in all scenarios.
          "vm.vfs_cache_pressure" = 1000; # Default: 100
        };


        # User Configuration
        users.users.${cfg.user} = {
          password = "";
          isNormalUser = true;
          createHome = true;
          home = "/home/${cfg.user}";
          extraGroups = [ "wheel" ];
          openssh.authorizedKeys.keys = cfg.sshAuthorizedKeys;
        };

        home-manager = {
          useGlobalPkgs = true;
          useUserPackages = true;
          users.${cfg.user} = {
            imports = cfg.homeModules;

            home.username = lib.mkDefault cfg.user;
            home.homeDirectory = lib.mkDefault "/home/${cfg.user}";
            home.stateVersion = lib.mkDefault config.system.stateVersion;

            programs.home-manager.enable = lib.mkDefault true;
          };
        };

        services.openssh = lib.mkIf (cfg.connectWith == "ssh") {
          enable = true;
          openFirewall = false;
          settings = {
            PasswordAuthentication = false;
            KbdInteractiveAuthentication = false;
            PermitRootLogin = "no";
          };
        };

        security.sudo.wheelNeedsPassword = false;
        swapDevices = lib.optionals (cfg.swapSize > 0) [
          {
            device = "/swapfile";
            size = cfg.swapSize;
          }
        ];

        # Directory permissions
        systemd.tmpfiles.rules = [
          "d /home/${cfg.user} 0700 ${cfg.user} users -"
        ];

        # Inbox-mounted directories are owned by root, but readable which we'll be
        # using to clone as local user.
        # TODO: mkOptional if we're using inboxes
        environment.etc."gitconfig".text = ''
          [safe]
            directory = /home/${cfg.user}/mnt/inbox/*
        '';

        # Basic Package Set
        environment.systemPackages = [
          agentspace-init
          agentspace-logout
          agentspace-bundle
        ]
        ++ (with pkgs; [
          bashInteractive
          coreutils
          curl
          fd
          git
          gnugrep
          less
          neovim
          which
        ]);

        microvm.binScripts.virtiofsd-run = lib.mkForce ''
          echo "virtiofsd-run is managed by virtie and should not be invoked directly." >&2
          exit 1
        '';

        # MicroVM Configuration
        microvm = {
          vcpu = lib.mkDefault 8;
          mem = lib.mkDefault (4 * 1024);
          balloon = true;
          socket = "/tmp/vm-${cfg.hostName}.sock";
          hypervisor = "qemu";

          qemu.serialConsole = false;

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
              readOnly = true;
            }
          ]
          ++ lib.optionals cfg.mountWorkspace [
            {
              proto = cfg.protocol;
              tag = "workspace";
              source = ".";
              mountPoint = cfg.workspaceMountPoint;
              securityModel = "mapped";
            }
          ];

          writableStoreOverlay = "/nix/.rw-store";

          volumes = [
            {
              image = cfg.persistence.storeOverlay;
              mountPoint = "/nix/.rw-store";
              size = 2 * 4096;
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
      }
    ]
  );
}
