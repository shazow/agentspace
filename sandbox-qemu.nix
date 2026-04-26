{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.agentspace.sandbox;
  balloonTransport =
    if (!lib.hasPrefix "microvm" config.microvm.qemu.machine) || config.microvm.shares != [ ] then
      "pci"
    else
      "mmio";
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

    balloon = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enable the virtio-balloon device and virtie's default runtime balloon controller.";
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

    nixStoreShareSocket = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "/var/run/virtiofs-nix-store.sock";
      description = ''
        Existing host-side virtiofsd socket for the Nix store share. If
        provided, QEMU uses this socket instead of having virtie start a
        userland virtiofsd process for the share.
      '';
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

    launch = {
      commonInit = lib.mkOption {
        type = lib.types.lines;
        readOnly = true;
        internal = true;
      };

      virtieManifestData = lib.mkOption {
        type = lib.types.anything;
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

      initDirs = lib.unique (builtins.map (volume: builtins.dirOf volume.image) config.microvm.volumes);

      commonInit = ''
        echo "🚀 Preparing Agent QEMU Sandbox..."
      ''
      + lib.optionalString cfg.mountWorkspace ''
        echo "📂 Mounting current directory at ~/workspace"
        cd "$REPO_DIR"
      '';

      sshBaseArgv = [
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
      baseQemuConfig = import ./agentspace-qemu-config.nix {
        inherit config lib pkgs;
      };
      nixStoreShareUsesSocket = cfg.nixStoreShareSocket != null;
      isNixStoreShare = share: share.tag == "ro-store";
      managedVirtioFSShares = builtins.filter (
        share: !(nixStoreShareUsesSocket && isNixStoreShare share)
      ) virtiofsShares;
      qemuConfig = baseQemuConfig // {
        devices =
          baseQemuConfig.devices
          // {
            virtiofs = builtins.map (
              share:
              if nixStoreShareUsesSocket && isNixStoreShare share then
                share // { socketPath = cfg.nixStoreShareSocket; }
              else
                share
            ) baseQemuConfig.devices.virtiofs;
          }
          // lib.optionalAttrs config.microvm.balloon {
            balloon = {
              id = "balloon0";
              transport = balloonTransport;
              deflateOnOOM = config.microvm.deflateOnOOM;
              freePageReporting = true;
            };
          };
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

          socket_path=${lib.escapeShellArg socket}
          if [ -n "''${VIRTIE_SOCKET_PATH-}" ]; then
            socket_path="$VIRTIE_SOCKET_PATH"
          fi

          exec ${lib.getExe config.microvm.virtiofsd.package} \
            --socket-path="$socket_path" \
            ${
              lib.optionalString (
                config.microvm.virtiofsd.group != null
              ) "--socket-group=${config.microvm.virtiofsd.group}"
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
      }) managedVirtioFSShares;

      manifestVolumes = builtins.map (volume: {
        imagePath = volume.image;
        sizeMiB = volume.size;
        fsType = volume.fsType;
        autoCreate = volume.autoCreate;
        label = volume.label;
        mkfsExtraArgs = volume.mkfsExtraArgs;
      }) config.microvm.volumes;

      virtieManifestData = {
        identity.hostName = cfg.hostName;
        paths = {
          workingDir = ".";
          lockPath = "/tmp/agentspace-${cfg.hostName}.lock";
          runtimeDir = "";
        };
        persistence.directories = initDirs;
        ssh = {
          argv = sshBaseArgv;
          user = cfg.user;
        };
        qemu = qemuConfig;
        volumes = manifestVolumes;
        virtiofs.daemons = virtiofsDaemons;
      };

      virtieManifest = pkgs.writeText "virtie-${cfg.hostName}.json" (builtins.toJSON virtieManifestData);
    in
    lib.mkMerge [
      {
        assertions = [
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
          inherit commonInit;
          inherit virtieManifestData;
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

        services.openssh = {
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

        # Basic Package Set
        environment.systemPackages = [
          agentspace-logout
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
          balloon = lib.mkDefault cfg.balloon;
          socket = "qmp.sock";
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
              proto = "virtiofs";
              tag = "ro-store";
              source = "/nix/store";
              mountPoint = "/nix/.ro-store";
              readOnly = true;
            }
          ]
          ++ lib.optionals cfg.mountWorkspace [
            {
              proto = "virtiofs";
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
