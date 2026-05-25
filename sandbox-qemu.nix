{
  config,
  lib,
  options,
  pkgs,
  mkExecSSH ? import ./lib/mkExecSSH.nix { inherit pkgs lib; },
  ...
}:

let
  cfg = config.agentspace.sandbox;
  persistenceBaseDir = cfg.persistence.baseDir;
  persistenceStateDir = persistenceBaseDir;
  resolvePersistencePath =
    path: if path == null || lib.hasPrefix "/" path then path else "${persistenceBaseDir}/${path}";
  resolvedHomeImage = resolvePersistencePath cfg.persistence.homeImage;
  resolvedStoreOverlay = resolvePersistencePath cfg.persistence.storeOverlay;
  resolvedSerialMode =
    if cfg.quiet then
      "off"
    else
      "print";
  workspaceHostDir = cfg.workspace.hostDir;
  workspaceSwapFile = "${cfg.workspace.guestDir}/swapfile";
  workspaceBaseShare = {
    proto = "virtiofs";
    tag = "workspace";
    source = workspaceHostDir;
    mountPoint = cfg.workspace.guestDir;
    securityModel = "mapped";
  };
  workspaceKeyToTag = key: "workspace-${lib.replaceStrings [ "/" "." " " ] [ "-" "-" "-" ] key}";
  workspaceSpaces = lib.optionalAttrs cfg.workspace.enable cfg.workspace.spaces;
  workspaceShares = lib.mapAttrsToList (key: source: {
    proto = "virtiofs";
    tag = workspaceKeyToTag key;
    inherit source;
    mountPoint = "${cfg.workspace.guestDir}/${key}";
    securityModel = "mapped";
  }) workspaceSpaces;
in
{
  imports = [
    ./unsupported.nix
  ];

  options.agentspace.sandbox = {
    enable = lib.mkEnableOption "Agent Sandbox MicroVM Environment";

    user = lib.mkOption {
      type = lib.types.str;
      default = "agent";
      description = "Username for the guest.";
    };

    groups = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [
        "wheel"
        "kvm"
      ];
      description = "Extra groups for the guest user.";
    };

    hostName = lib.mkOption {
      type = lib.types.str;
      default = "agent-sandbox";
      description = "Hostname for the guest VM.";
    };

    quiet = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Quiet kernel output. Disable to debug boot issues.";
    };

    machine = {
      memory = lib.mkOption {
        type = lib.types.ints.positive;
        default = 4 * 1024;
        description = "Memory size for the guest VM in MiB.";
      };

      vcpu = lib.mkOption {
        type = lib.types.nullOr lib.types.ints.positive;
        default = null;
        description = "Number of vCPUs for the guest VM. Null lets virtie use the host-visible CPU count at launch time.";
      };
    };

    ssh = {
      authorizedKeys = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Setup ssh access with these authorized keys.";
      };

      command = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Default remote shell command passed to the sandbox SSH session.";
      };

      exec = lib.mkOption {
        type = lib.types.nullOr (lib.types.listOf lib.types.str);
        default = null;
        example = [
          "/usr/bin/ssh"
          "-F"
          "ssh_config"
        ];
        description = ''
          Complete host-side SSH argv used by virtie. When unset, agentspace
          generates a default OpenSSH command. Use `lib.mkExecSSH` to build the
          common OpenSSH argv with optional config and identity arguments.
        '';
      };

      autoconnect = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Whether the generated launch wrapper should attach an SSH session automatically.";
      };

    };

    persistence = {
      baseDir = lib.mkOption {
        type = lib.types.str;
        default = ".agentspace";
        description = "Base directory for generated sandbox persistence paths.";
      };

      # TODO: Remove this after 2026-06-01
      basedir = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        visible = false;
        description = "Deprecated spelling of baseDir.";
      };

      homeImage = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = "home.img";
        description = "Path to the ext4 disk image for home directory persistence. Set to null to disable.";
      };

      homeSize = lib.mkOption {
        type = lib.types.ints.positive;
        default = 4096;
        description = "Size of persistent home image in MB.";
      };

      storeOverlay = lib.mkOption {
        type = lib.types.str;
        default = "nix-store-overlay.img";
        description = "Path for the writable nix store overlay image.";
      };

      storeDisk = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = "Whether to boot from a generated read-only Nix store disk instead of a host store share.";
      };
    };

    workspace = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Enable workspace-oriented host directory mounts.";
      };

      guestDir = lib.mkOption {
        type = lib.types.str;
        default = "/home/${cfg.user}/workspace";
        description = "Guest directory containing workspace spaces and exported as WORKSPACE.";
      };

      hostDir = lib.mkOption {
        type = lib.types.str;
        default = "${persistenceStateDir}/workspace";
        description = "Host directory backing the default workspace share.";
      };

      addCurrentDir = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Include the launch-time $PWD as a workspace";
      };

      spaces = lib.mkOption {
        type = lib.types.attrsOf lib.types.str;
        default = { };
        example = {
          agentspace = "/home/example/projects/agentspace";
          "project2/foo" = "/home/example/foo";
        };
        description = "Host directories to mount";
      };
    };

    shares = lib.mkOption {
      type = options.microvm.shares.type;
      default = [ ];
      description = "Additional host directory shares mounted in the sandbox, using the microvm.shares schema.";
    };

    run = lib.mkOption {
      type = lib.types.listOf (
        lib.types.submodule (
          { ... }:
          {
            options = {
              exec = lib.mkOption {
                type = lib.types.nonEmptyListOf lib.types.str;
                example = [
                  "xdg-dbus-proxy"
                  "{{.Env.DBUS_SESSION_BUS_ADDRESS}}"
                  "{{.Workspace.HostPath}}/dbus-proxy.sock"
                  "--filter"
                ];
                description = "Host-side argv run and managed by virtie. Template variables include Workspace.GuestPath, Workspace.HostPath, CID, StateDir, vars entries, and Env.";
              };

              vars = lib.mkOption {
                type = lib.types.attrsOf lib.types.anything;
                default = { };
                description = "Additional template variables exposed to this run command.";
              };
            };
          }
        )
      );
      default = [ ];
      description = "Host-side commands managed for the lifetime of the virtie launch.";
    };

    swapSize = lib.mkOption {
      type = lib.types.ints.unsigned;
      default = 0;
      description = "Size of the guest sparse swapfile in MiB. Set to 0 to disable (e.g. 4096).";
    };

    balloon = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enable the virtio-balloon device and virtie's default runtime balloon controller.";
    };

    notifications = {
      command = lib.mkOption {
        type = lib.types.str;
        default = "";
        example = ''
          notify-send "virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE"
        '';
        description = "Host-side shell command for virtie runtime notification hooks. Set to an empty string to disable notifications.";
      };

      states = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Optional notification state allowlist. Empty means all states.";
      };
    };

    writeFiles = lib.mkOption {
      type = lib.types.attrsOf (
        lib.types.submodule (
          { ... }:
          {
            options = {
              text = lib.mkOption {
                type = lib.types.nullOr lib.types.str;
                default = null;
                example = ''{"foo": "bar"}'';
                description = "Plaintext to write into the guest file. Use path for binary files.";
              };

              chown = lib.mkOption {
                type = lib.types.nullOr lib.types.str;
                default = null;
                example = "agent:users";
                description = "Optional user:group ownership value applied after writing the guest file.";
              };

              path = lib.mkOption {
                type = lib.types.nullOr lib.types.str;
                default = null;
                description = "Host path whose bytes are written into the guest file.";
              };

              followLinks = lib.mkOption {
                type = lib.types.bool;
                default = true;
                description = "Whether to follow symlinks when reading the host path.";
              };

              writeBack = lib.mkOption {
                type = lib.types.bool;
                default = false;
                description = "Whether to write guest file contents back to the host path on shutdown or suspend.";
              };

              mode = lib.mkOption {
                type = lib.types.nullOr lib.types.str;
                default = null;
                example = "0644";
                description = "Optional four-digit octal permission mode applied after writing the guest file.";
              };

              overwrite = lib.mkOption {
                type = lib.types.bool;
                default = false;
                description = "Whether to overwrite the guest file when it already exists.";
              };
            };
          }
        )
      );
      default = { };
      description = "Files to write into the guest during fresh VM launch, keyed by absolute guest path.";
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
        type = lib.types.str;
        readOnly = true;
        internal = true;
      };

      virtieManifestTemplate = lib.mkOption {
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
      + lib.optionalString cfg.workspace.enable ''
        ${pkgs.coreutils}/bin/mkdir -p ${lib.escapeShellArg workspaceHostDir}
      ''
      + lib.optionalString nixStoreShareUsesSocket ''
        if [ ! -S ${lib.escapeShellArg cfg.nixStoreShareSocket} ]; then
          echo "agentspace: nixStoreShareSocket does not exist or is not a socket: ${lib.escapeShellArg cfg.nixStoreShareSocket}" >&2
          exit 1
        fi
      ''
      + lib.optionalString (cfg.workspace.enable && cfg.workspace.addCurrentDir) ''
        echo "📂 Mounting current directory into workspace"
        cd "$REPO_DIR"
      '';

      sshAutoprovision = cfg.ssh.exec == null && cfg.ssh.authorizedKeys == [ ];
      defaultSSHExec = mkExecSSH { };
      sshExec = if cfg.ssh.exec != null then cfg.ssh.exec else defaultSSHExec;
      nixStoreShareUsesSocket = cfg.nixStoreShareSocket != null;
      isNixStoreShare = share: share.tag == "ro-store";
      virtiofsShares = builtins.filter (
        share: share.proto == "virtiofs" && !(cfg.persistence.storeDisk && isNixStoreShare share)
      ) config.microvm.shares;
      ninepShares = builtins.filter (share: share.proto == "9p") config.microvm.shares;
      inherit (pkgs.stdenv.hostPlatform) system;
      arch = builtins.head (builtins.split "-" system);
      canSandbox = builtins.elem "--enable-seccomp" (config.microvm.qemu.package.configureFlags or [ ]);

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
          if [ -n "''${VIRTIOFSD_SOCKET-}" ]; then
            socket_path="$VIRTIOFSD_SOCKET"
          fi

          exec ${lib.getExe config.microvm.virtiofsd.package} \
            --socket-path="$socket_path" \
            ${
              lib.optionalString (
                config.microvm.virtiofsd.group != null
              ) "--socket-group=${config.microvm.virtiofsd.group}"
            } \
            --shared-dir="''${VIRTIOFSD_SOURCE-${lib.escapeShellArg source}}" \
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

      notificationManifest = {
        states = cfg.notifications.states;
      }
      // lib.optionalAttrs (cfg.notifications.command != "") {
        exec = [
          pkgs.runtimeShell
          "-c"
          cfg.notifications.command
        ];
      };

      mkManifestVolume =
        volume:
        {
          image = volume.image;
          size = volume.size;
          fs = volume.fsType;
          create = volume.autoCreate;
          read_only = volume.readOnly;
          direct = volume.direct;
        }
        // lib.optionalAttrs (volume.label != null) { label = volume.label; }
        // lib.optionalAttrs (volume.serial != null) { serial = volume.serial; };
      manifestVolumes =
        lib.optionals cfg.persistence.storeDisk [
          {
            image = config.microvm.storeDisk;
            read_only = true;
          }
        ]
        ++ builtins.map mkManifestVolume config.microvm.volumes;

      manifestForwardPorts = builtins.map (
        {
          proto,
          from,
          host,
          guest,
        }:
        {
          inherit proto from;
          host = "${host.address}:${toString host.port}";
          guest = "${guest.address}:${toString guest.port}";
        }
      ) config.microvm.forwardPorts;

      manifestWorkspace = lib.optionalAttrs cfg.workspace.enable (
        {
          guest_dir = cfg.workspace.guestDir;
          host_dir = cfg.workspace.hostDir;
        }
        // lib.optionalAttrs cfg.workspace.addCurrentDir {
          mount_cwd = true;
        }
      );

      runConfigVars = {
        user = cfg.user;
        hostName = cfg.hostName;
        workspace = {
          enable = cfg.workspace.enable;
          guestDir = cfg.workspace.guestDir;
          hostDir = cfg.workspace.hostDir;
          addCurrentDir = cfg.workspace.addCurrentDir;
          spaces = cfg.workspace.spaces;
        };
        persistence = {
          baseDir = cfg.persistence.baseDir;
          homeImage = cfg.persistence.homeImage;
          homeSize = cfg.persistence.homeSize;
          storeOverlay = cfg.persistence.storeOverlay;
          storeDisk = cfg.persistence.storeDisk;
        };
      };

      manifestRun = builtins.map (run: {
        exec = run.exec;
        vars = run.vars // {
          Config = runConfigVars;
        };
      }) cfg.run;

      manifestMounts =
        builtins.map (
          share:
          let
            socketPath =
              if nixStoreShareUsesSocket && isNixStoreShare share then cfg.nixStoreShareSocket else share.socket;
          in
          {
            type = "virtiofs";
            tag = share.tag;
            source = share.source;
            read_only = share.readOnly;
            security_model = share.securityModel;
            cache = share.cache;
            virtiofs = (
              {
                socket = socketPath;
              }
              // lib.optionalAttrs (!(nixStoreShareUsesSocket && isNixStoreShare share)) {
                bin = mkVirtioFSDaemonCommand share;
                args = [ ];
              }
            );
          }
        ) virtiofsShares
        ++ builtins.map (share: {
          type = "9p";
          tag = share.tag;
          source = share.source;
          read_only = share.readOnly;
          security_model = share.securityModel;
        }) ninepShares;

      manifestWriteFiles = lib.mapAttrsToList (
        guestPath: file:
        {
          guest_path = guestPath;
          follow_links = file.followLinks;
          write_back = file.writeBack;
        }
        // lib.optionalAttrs (file.chown != null) { chown = file.chown; }
        // lib.optionalAttrs (file.text != null) { text = file.text; }
        // lib.optionalAttrs (file.mode != null) { mode = file.mode; }
        // lib.optionalAttrs (file.overwrite != null) { overwrite = file.overwrite; }
        // lib.optionalAttrs (file.path != null) { source = file.path; }
      ) cfg.writeFiles;

      virtieManifestData = {
        host_name = cfg.hostName;
        working_dir = ".";
        state_dir = persistenceStateDir;
        qemu = {
          exec = [
            "${config.microvm.qemu.package}/bin/qemu-system-${arch}"
          ]
          ++ config.microvm.qemu.extraArgs;
          fwd_tunnel_exec = [
            "${config.microvm.vmHostPackages.netcat}/bin/nc"
            "{{.Host}}"
            "{{.Port}}"
          ];
          seccomp = canSandbox;
          qmp_socket = if config.microvm.socket != null then config.microvm.socket else "qmp.sock";
          guest_agent_socket = "qga.sock";
        }
        // lib.optionalAttrs (config.microvm.user != null) {
          user = config.microvm.user;
        }
        // lib.optionalAttrs (config.microvm.qemu.machineOpts != null) {
          machine_options = config.microvm.qemu.machineOpts;
        };
        machine = {
          type = config.microvm.qemu.machine;
          memory = config.microvm.mem;
          kvm = pkgs.stdenv.hostPlatform.isLinux && config.microvm.cpu == null;
        }
        // lib.optionalAttrs (cfg.machine.vcpu != null) {
          vcpu = cfg.machine.vcpu;
        }
        // lib.optionalAttrs (config.microvm.machineId != null) {
          id = config.microvm.machineId;
        }
        // lib.optionalAttrs (config.microvm.cpu != null) {
          cpu = config.microvm.cpu;
        };
        kernel = {
          path = "${config.microvm.kernel.out}/${pkgs.stdenv.hostPlatform.linux-kernel.target}";
          initrd_path = config.microvm.initrdPath;
          params = config.microvm.kernelParams;
          serial = resolvedSerialMode;
        };
        graphics = {
          backend = if config.microvm.graphics.enable then config.microvm.graphics.backend else "headless";
        };
        ssh = {
          exec = sshExec;
          user = cfg.user;
          ready_socket = "ready.sock";
          autoprovision = sshAutoprovision;
        };
        balloon =
          if config.microvm.balloon then
            {
              enabled = true;
              deflate_on_oom = config.microvm.deflateOnOOM;
              free_page_reporting = true;
            }
          else
            {
              enabled = false;
            };
        volumes = manifestVolumes;
        mounts = manifestMounts;
        workspace = manifestWorkspace;
        networks = builtins.map (interface: {
          id = interface.id;
          type = interface.type;
          mac = interface.mac;
          forward = manifestForwardPorts;
        }) config.microvm.interfaces;
        notifications = notificationManifest;
        write_files = manifestWriteFiles;
        run = manifestRun;
      };

      tomlFormat = pkgs.formats.toml { };
      virtieManifestTemplate = tomlFormat.generate "virtie-${cfg.hostName}.toml" virtieManifestData;
      virtieManifest = "${persistenceBaseDir}/virtie-${cfg.hostName}.toml";
    in
    lib.mkMerge [
      {
        assertions = [
          {
            assertion = cfg.persistence.basedir == null;
            message = "agentspace.sandbox.persistence.basedir was renamed to agentspace.sandbox.persistence.baseDir.";
          }
          {
            assertion = cfg.swapSize == 0 || cfg.workspace.enable;
            message = "agentspace.sandbox.swapSize requires agentspace.sandbox.workspace.enable = true because the swapfile is created under WORKSPACE.";
          }
          {
            assertion =
              cfg.nixStoreShareSocket == null
              || (cfg.nixStoreShareSocket != "" && lib.hasPrefix "/" cfg.nixStoreShareSocket);
            message = "agentspace.sandbox.nixStoreShareSocket must be an absolute socket path when set.";
          }
        ];

        agentspace.sandbox.launch = {
          inherit commonInit;
          inherit virtieManifestData;
          inherit virtieManifest;
          inherit virtieManifestTemplate;
        };

        networking.hostName = cfg.hostName;
        nixpkgs.config.allowUnfree = true;
        services.logrotate.enable = false;
        systemd.services."serial-getty@ttyS0".enable = lib.mkIf (resolvedSerialMode == "print") false;
        systemd.services."serial-getty@ttyAMA0".enable = lib.mkIf (resolvedSerialMode == "print") false;

        # Boot & Kernel
        boot.kernel.sysctl."kernel.unprivileged_userns_clone" = 1;
        boot.kernelParams = lib.mkIf cfg.quiet [
          "quiet"
          "udev.log_level=3"
        ];
        boot.consoleLogLevel = lib.mkIf cfg.quiet 0;
        boot.initrd.verbose = lib.mkIf cfg.quiet false;
        boot.tmp.useTmpfs = false;

        # Some workspace-backed filesystems (like FUSE) reject chgrp/chown on
        # the virtiofsd socket path with EINVAL when --socket-group is used.
        # virtie starts QEMU and virtiofsd as the same user, so managed
        # sockets do not need a group override.
        microvm.virtiofsd.group = lib.mkDefault null;

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

        environment.sessionVariables = lib.mkIf cfg.workspace.enable {
          WORKSPACE = cfg.workspace.guestDir;
        };

        # User Configuration
        users.users.${cfg.user} = {
          password = "";
          isNormalUser = true;
          createHome = true;
          home = "/home/${cfg.user}";
          extraGroups = cfg.groups;
          openssh.authorizedKeys.keys = cfg.ssh.authorizedKeys;
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
        systemd.services.virtie-ssh-signal = {
          wantedBy = [ "multi-user.target" ];
          requires = [ "sshd.service" ];
          after = [ "sshd.service" ];
          serviceConfig.Type = "oneshot";
          script = ''
            ${pkgs.coreutils}/bin/echo SSH-READY > /dev/virtio-ports/virtie.ready
          '';
        };
        services.qemuGuest.enable = true;

        security.sudo.wheelNeedsPassword = false;
        swapDevices = lib.optionals (cfg.swapSize > 0) [
          {
            device = workspaceSwapFile;
            size = cfg.swapSize;
          }
        ];

        # Directory permissions
        systemd.tmpfiles.rules = [
          "d /home/${cfg.user} 0700 ${cfg.user} users -"
        ]
        ++ lib.optionals cfg.workspace.enable [
          "d ${cfg.workspace.guestDir} 0755 ${cfg.user} users -"
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
          storeOnDisk = cfg.persistence.storeDisk;
          mem = cfg.machine.memory;
          vcpu = lib.mkIf (cfg.machine.vcpu != null) cfg.machine.vcpu;
          balloon = lib.mkDefault cfg.balloon;
          socket = "qmp.sock";
          hypervisor = "qemu";

          qemu.serialConsole = lib.mkDefault false;

          interfaces = [
            {
              type = "user";
              id = "microvm1";
              mac = "02:02:00:00:00:01";
            }
          ];

          shares =
            lib.optionals (!cfg.persistence.storeDisk) [
              {
                proto = "virtiofs";
                tag = "ro-store";
                source = "/nix/store";
                mountPoint = "/nix/.ro-store";
                readOnly = true;
              }
            ]
            ++ lib.optionals cfg.workspace.enable [
              workspaceBaseShare
            ]
            ++ lib.optionals (cfg.workspace.enable && cfg.workspace.addCurrentDir) [
              {
                proto = "virtiofs";
                tag = "workspace_cwd";
                source = ".";
                mountPoint = "/mnt/cwd";
                securityModel = "mapped";
              }
            ]
            ++ workspaceShares
            ++ cfg.shares;

          writableStoreOverlay = "/nix/.rw-store";

          volumes = [
            {
              image = resolvedStoreOverlay;
              mountPoint = "/nix/.rw-store";
              size = 2 * 4096;
            }
          ]
          ++ lib.optionals (cfg.persistence.homeImage != null) [
            {
              image = resolvedHomeImage;
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
