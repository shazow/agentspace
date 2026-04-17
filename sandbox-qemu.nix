{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.agentspace.sandbox;
  readinessEnabled = cfg.connectWith == "ssh";
  readinessPort = 17999;
  readinessTarget = "agentspace-ssh-ready.target";
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
      default = "console";
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
      description = "Host-side private key path used by SSH-based launch and connect commands.";
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

      initDirs =
        lib.pipe
          [ cfg.persistence.homeImage cfg.persistence.storeOverlay ]
          [
            (builtins.filter (x: x != null))
            (builtins.map builtins.dirOf)
            lib.escapeShellArgs
          ];
    in
    lib.mkMerge [
      {
        agentspace.sandbox.initExtra = lib.mkAfter (
          lib.optionalString (initDirs != "") ''
            mkdir -vp ${initDirs}
          ''
          + ''
            name_prefix="agentspace-${cfg.hostName}";
            vfs_unit="$name_prefix-virtiofsd"
            vm_unit="$name_prefix-vm"
            connect_unit="$name_prefix-connect"
            tracked_units=()

            if systemctl --user --quiet is-active "$name_prefix-*.service"; then
              echo "launch-agent: refusing to start because an agentspace unit is already active" >&2
              exit 1
            fi
            systemctl --user reset-failed "$name_prefix-*.service"
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
            tracked_units+=("$vm_unit.service")
            ready_pid=""
            readiness_fallback=0
            readiness_attempts=0
            ssh_proxy="${pkgs.socat}/bin/socat STDIO VSOCK-CONNECT:${toString config.microvm.vsock.cid}:22"

            cleanup() {
              if [ -n "$ready_pid" ] && kill -0 "$ready_pid" >/dev/null 2>&1; then
                kill "$ready_pid" >/dev/null 2>&1 || true
              fi
              if [ "''${#tracked_units[@]}" -gt 0 ]; then
                systemctl --user stop "''${tracked_units[@]}" >/dev/null 2>&1 || true
              fi
            }
            trap cleanup EXIT INT TERM

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

            echo "⏳ Waiting for guest SSH readiness..."
            while true; do
              ${pkgs.nmap}/bin/ncat -l --vsock --udp 2 "${toString readinessPort}" \
                | ${pkgs.gnugrep}/bin/grep -m1 -Fx "X_SYSTEMD_UNIT_ACTIVE=${readinessTarget}" >/dev/null &
              ready_pid=$!

              sleep 0.1
              if kill -0 "$ready_pid" >/dev/null 2>&1; then
                break
              fi

              if wait "$ready_pid"; then
                ready_pid=""
                break
              fi

              if ! systemctl --user --quiet is-active "$vm_unit.service"; then
                echo "launch-agent: microvm terminated before SSH readiness listener could start" >&2
                exit 1
              fi

              readiness_attempts=$((readiness_attempts + 1))
              if [ "$readiness_attempts" -ge 50 ]; then
                readiness_fallback=1
                echo "launch-agent: host vsock listener unavailable, falling back to SSH retries" >&2
                break
              fi
            done

            if [ "$readiness_fallback" = 0 ] && [ -n "$ready_pid" ]; then
              if ! wait "$ready_pid"; then
                echo "launch-agent: guest SSH readiness wait failed" >&2
                exit 1
              fi
              ready_pid=""
              echo "✅ Guest SSH readiness received."
            fi

            echo "🔐 Starting SSH..."
            ssh_tty_args=()
            if [ "$#" -eq 0 ]; then
              ssh_tty_args=(-tt)
            fi
            ssh_deadline=$((SECONDS + 60))
            while true; do
              set +e
              ${pkgs.openssh}/bin/ssh \
                -F /dev/null \
                "''${ssh_tty_args[@]}" \
                ${lib.optionalString (
                  cfg.sshIdentityFile != null
                ) "-i ${lib.escapeShellArg cfg.sshIdentityFile} \\"}
                ${lib.optionalString (cfg.sshIdentityFile != null) "-o IdentitiesOnly=yes \\"}
                -o ProxyCommand="$ssh_proxy" \
                -o StrictHostKeyChecking=no \
                -o UserKnownHostsFile=/dev/null \
                -o GlobalKnownHostsFile=/dev/null \
                "${cfg.user}@agentspace" \
                "$@"
              ssh_status=$?
              set -e

              if [ "$ssh_status" -ne 255 ] || [ "$readiness_fallback" = 0 ]; then
                exit "$ssh_status"
              fi
              if [ "$SECONDS" -ge "$ssh_deadline" ]; then
                echo "launch-agent: SSH fallback retries timed out" >&2
                exit "$ssh_status"
              fi

              sleep 1
            done

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

        systemd.targets = lib.mkIf readinessEnabled {
          ${readinessTarget} = {
            description = "Agentspace SSH readiness target";
            requires = [ "sshd.service" ];
            after = [ "sshd.service" ];
            wantedBy = [ "multi-user.target" ];
          };
        };

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
          qemu.extraArgs = lib.optionals readinessEnabled [
            "-smbios"
            "type=11,value=io.systemd.credential:vmm.notify_socket=vsock-dgram:2:${toString readinessPort}"
          ];

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
