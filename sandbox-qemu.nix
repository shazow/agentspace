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

    connectWith = lib.mkOption {
      type = lib.types.enum [
        "console"
        "ssh"
      ];
      default = "console";
      description = "Auto-connect via serial console or ssh. When using ssh, make sure to set sshAuthorizedKeys.";
    };

    sshAuthorizedKeys = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Setup ssh access with these authorized keys.";
    };

    workspaceMountPoint = lib.mkOption {
      type = lib.types.str;
      default = "/home/${cfg.user}/workspace";
      description = "Where to mount the current working directory inside the VM.";
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
    in
    lib.mkMerge [
      {
        agentspace.sandbox.initExtra = lib.mkAfter (
          ''
            name_prefix="agentspace-${cfg.hostName}";
            vfs_unit="$name_prefix-virtiofsd"
            vm_unit="$name_prefix-vm"
            connect_unit="$name_prefix-connect"
            tracked_units=()

            if systemctl --user --quiet is-active "$name_prefix-*.service"; then
              echo "launch-agent: refusing to start because an agentspace unit is already active" >&2
              exit 1
            fi
          ''
          + lib.optionalString (cfg.protocol == "virtiofs") ''
            echo "📦 Starting virtiofsd..."
            tracked_units+=("$vfs_unit.service")
            systemd-run --user \
              --unit="$vfs_unit" \
              --collect \
              --service-type=exec \
              -p KillMode=control-group \
              -p Restart=on-failure \
              -p RestartSec=500ms \
              -p TimeoutStopSec=15s \
              -p WorkingDirectory="$REPO_DIR" \
              "$RUNNER_PATH/bin/virtiofsd-run"
            trap 'systemctl --user stop "$vfs_unit.service" >/dev/null 2>&1 || true' EXIT INT TERM
          ''
          + lib.optionalString (cfg.connectWith == "ssh") ''
            tracked_units+=("$connect_unit.service" "$vm_unit.service")

            # FIXME: Do we need this?
            #systemctl --user reset-failed "''${tracked_units[@]}"

            echo "🖥️  Starting microvm..."
            systemd-run --user \
              --unit="$vm_unit" \
              --collect \
              --service-type=exec \
              ${lib.optionalString (cfg.protocol == "virtiofs") "-p BindsTo=\"$vfs_unit.service\""} \
              ${lib.optionalString (cfg.protocol == "virtiofs") "-p After=\"$vfs_unit.service\""} \
              -p KillMode=control-group \
              -p TimeoutStopSec=15s \
              -p WorkingDirectory="$REPO_DIR" \
              "$RUNNER_PATH/bin/microvm-run"

            echo "🔐 Starting SSH..."
            systemd-run --user \
              --unit="$connect_unit" \
              --collect \
              --wait \
              --pipe \
              --service-type=exec \
              -p BindsTo="$vm_unit.service" \
              -p After="$vm_unit.service" \
              -p Restart=on-failure \
              -p RestartSec=1000ms \
              -p StartLimitIntervalSec=0 \
              -p KillMode=control-group \
              -p TimeoutStopSec=15s \
              ${pkgs.openssh}/bin/ssh \
              -tt \
              -o StrictHostKeyChecking=no \
              -o UserKnownHostsFile=/dev/null \
              -o GlobalKnownHostsFile=/dev/null \
              "${cfg.user}@vsock/${toString config.microvm.vsock.cid}" \
              "$@"

            systemctl --user stop "''${tracked_units[@]}"
            exit
          ''
        );

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

        # User Configuration
        users.users.${cfg.user} = {
          password = "";
          isNormalUser = true;
          extraGroups = [ "wheel" ];
          openssh.authorizedKeys.keys = cfg.sshAuthorizedKeys;
        };

        services.openssh = lib.mkIf (cfg.sshAuthorizedKeys != [ ]) {
          enable = true;
          openFirewall = false;
          settings = {
            PasswordAuthentication = false;
            KbdInteractiveAuthentication = false;
            PermitRootLogin = "no";
          };
        };

        security.sudo.wheelNeedsPassword = false;

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

        # MicroVM Configuration
        microvm = {
          vcpu = lib.mkDefault 8;
          mem = lib.mkDefault (4 * 1024);
          balloon = true;
          socket = "/tmp/vm-${cfg.hostName}.sock";
          hypervisor = "qemu";

          qemu.serialConsole = cfg.connectWith == "console";

          vsock = {
            cid = lib.mkDefault 10;
            ssh.enable = true;
          };

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
      }

      (lib.mkIf (cfg.connectWith == "console") {
        services.getty.autologinUser = cfg.user;
        systemd.tmpfiles.rules = lib.mkAfter [
          "f /home/${cfg.user}/.bash_logout 0600 ${cfg.user} users - agentspace-logout"
        ];
      })
    ]
  );
}
