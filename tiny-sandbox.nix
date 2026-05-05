{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.agentspace.tinySandbox;

  persistenceBaseDir = cfg.persistence.basedir;

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
  ++ lib.optionals (cfg.ssh.identityFile != null) [
    "-i"
    cfg.ssh.identityFile
  ];

  authorizedKeys = lib.concatStringsSep "\n" cfg.ssh.authorizedKeys;

  baseQemuConfig = import ./agentspace-qemu-config.nix {
    inherit config lib pkgs;
  };
  qemuConfig = baseQemuConfig // {
    smp = lib.optionalAttrs (cfg.machine.vcpu != null) {
      cpus = cfg.machine.vcpu;
    };
  };
  extraUtils = config.system.build.extraUtils;
  opensshLibexecCompat = "/nix/store/eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee-openssh-${pkgs.openssh.version}/libexec";

  virtiofsShares = builtins.filter (share: share.proto == "virtiofs") config.microvm.shares;

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
  }) virtiofsShares;

  virtieManifestData = {
    identity.hostName = cfg.hostName;
    paths = {
      workingDir = ".";
      lockPath = "/tmp/agentspace-${cfg.hostName}.lock";
      runtimeDir = "";
    };
    persistence = {
      directories = [ ];
      baseDir = persistenceBaseDir;
      stateDir = persistenceBaseDir;
    };
    ssh = {
      argv = sshBaseArgv;
      user = cfg.user;
    };
    qemu = qemuConfig;
    volumes = [ ];
    virtiofs.daemons = virtiofsDaemons;
    writeFiles = cfg.writeFiles;
    notifications.states = [ ];
  };

  virtieManifestTemplate = pkgs.writeText "virtie-${cfg.hostName}.json" (
    builtins.toJSON virtieManifestData
  );
in
{
  options.agentspace.tinySandbox = {
    enable = lib.mkEnableOption "experimental initrd-only Agent Sandbox appliance";

    hostName = lib.mkOption {
      type = lib.types.str;
      default = "agent-tiny-sandbox";
      description = "Hostname for the tiny guest VM.";
    };

    user = lib.mkOption {
      type = lib.types.str;
      default = "agent";
      description = "Username for the tiny guest SSH session.";
    };

    machine = {
      memory = lib.mkOption {
        type = lib.types.ints.positive;
        default = 256;
        description = "Memory size for the tiny guest in MiB.";
      };

      vcpu = lib.mkOption {
        type = lib.types.nullOr lib.types.ints.positive;
        default = null;
        description = "Number of vCPUs for the tiny guest. Null lets virtie use the host-visible CPU count at launch time.";
      };
    };

    ssh = {
      authorizedKeys = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "SSH public keys admitted to the initrd appliance.";
      };

      command = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "Default remote shell command passed to the tiny SSH session.";
      };

      identityFile = lib.mkOption {
        type = lib.types.nullOr lib.types.str;
        default = null;
        description = "Optional SSH identity file passed to host-side launch/connect helpers.";
      };

      autoconnect = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Whether the generated launch wrapper should attach an SSH session automatically.";
      };
    };

    persistence.basedir = lib.mkOption {
      type = lib.types.str;
      default = ".agentspace";
      description = "Base directory for generated tiny sandbox runtime manifest paths.";
    };

    mountWorkspace = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Mount the current working directory into the tiny VM as the workspace share.";
    };

    writeFiles = lib.mkOption {
      type = lib.types.attrsOf (
        lib.types.submodule (
          { ... }:
          {
            options = {
              content = lib.mkOption {
                type = lib.types.nullOr lib.types.str;
                default = null;
                description = "Base64-encoded content to write into the guest file.";
              };

              chown = lib.mkOption {
                type = lib.types.nullOr lib.types.str;
                default = null;
                description = "Optional user:group ownership value applied after writing the guest file.";
              };

              path = lib.mkOption {
                type = lib.types.nullOr lib.types.str;
                default = null;
                description = "Host path whose bytes are base64-encoded and written into the guest file.";
              };

              mode = lib.mkOption {
                type = lib.types.nullOr lib.types.str;
                default = null;
                description = "Optional four-digit octal permission mode applied after writing the guest file.";
              };
            };
          }
        )
      );
      default = { };
      description = "Files to write into the tiny guest during fresh VM launch, keyed by absolute guest path.";
    };

    workspaceMountPoint = lib.mkOption {
      type = lib.types.str;
      default = "/home/${cfg.user}/workspace";
      description = "Where to mount the current working directory inside the tiny VM.";
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

  config = lib.mkIf cfg.enable {
    agentspace.tinySandbox.launch = {
      commonInit = ''
        echo "Preparing experimental tiny Agent QEMU Sandbox..."
      ''
      + lib.optionalString cfg.mountWorkspace ''
        cd "$REPO_DIR"
      '';
      inherit virtieManifestData virtieManifestTemplate;
      virtieManifest = "${persistenceBaseDir}/virtie-${cfg.hostName}.json";
    };

    networking.hostName = cfg.hostName;
    system.stateVersion = "25.11";

    fileSystems."/" = {
      device = "tmpfs";
      fsType = "tmpfs";
      neededForBoot = true;
      options = [
        "mode=0755"
        "size=64M"
      ];
    };

    boot.loader.grub.enable = false;
    boot.initrd.systemd.enable = false;
    boot.initrd.verbose = false;
    boot.consoleLogLevel = 4;
    boot.kernelParams = [
      "panic=-1"
      "reboot=t"
    ];
    boot.initrd.availableKernelModules = [
      "virtio"
      "virtio_ring"
      "virtio_mmio"
      "virtio_pci"
      "virtio_console"
      "virtiofs"
      "fuse"
      "vsock"
      "vmw_vsock_virtio_transport_common"
      "vmw_vsock_virtio_transport"
    ];

    boot.initrd.extraUtilsCommands = ''
      copy_bin_and_libs ${pkgs.busybox}/bin/busybox
      copy_bin_and_libs ${pkgs.socat}/bin/socat
      copy_bin_and_libs ${pkgs.socat}/bin/socat1
      copy_bin_and_libs ${pkgs.openssh}/bin/ssh
      copy_bin_and_libs ${pkgs.openssh}/bin/scp
      copy_bin_and_libs ${pkgs.openssh}/bin/sftp
      copy_bin_and_libs ${pkgs.openssh}/bin/ssh-keygen
      copy_bin_and_libs ${pkgs.openssh}/bin/sshd
      copy_bin_and_libs ${pkgs.openssh}/libexec/sftp-server
      copy_bin_and_libs ${pkgs.openssh}/libexec/sshd-auth
      copy_bin_and_libs ${pkgs.openssh}/libexec/sshd-session
      copy_bin_and_libs ${pkgs.qemu.ga}/bin/qemu-ga

      mkdir -p $out/etc/ssh
    '';

    boot.initrd.preDeviceCommands = lib.mkBefore ''
      exec >/dev/console 2>&1
      export PATH=/bin:/sbin:$PATH
      echo "tiny-sandbox: starting initrd appliance"

      mount -t proc proc /proc || true
      mount -t sysfs sysfs /sys || true
      mount -t devtmpfs devtmpfs /dev || true
      mkdir -p /dev/pts
      mount -t devpts devpts /dev/pts || true

      mkdir -p /etc/ssh /run/sshd /var/empty /home/${cfg.user}/.ssh /tmp
      mkdir -p ${opensshLibexecCompat}
      ln -sf ${extraUtils}/bin/sshd-auth ${opensshLibexecCompat}/sshd-auth
      ln -sf ${extraUtils}/bin/sshd-session ${opensshLibexecCompat}/sshd-session
      mkdir -p /run/current-system/sw/bin
      ln -sf ${extraUtils}/bin/busybox /run/current-system/sw/bin/chmod
      ln -sf ${extraUtils}/bin/busybox /run/current-system/sw/bin/chown
      ln -sf ${extraUtils}/bin/busybox /run/current-system/sw/bin/install
      ln -sf ${extraUtils}/bin/busybox /run/current-system/sw/bin/ps
      ln -sf ${extraUtils}/bin/busybox /run/current-system/sw/bin/test
      chmod 0755 /run/sshd /var/empty
      chmod 0700 /home/${cfg.user} /home/${cfg.user}/.ssh

      cat > /etc/passwd <<'EOF'
root:x:0:0:root:/root:/bin/sh
${cfg.user}:x:1000:100:Agent:/home/${cfg.user}:/bin/sh
sshd:x:74:74:Privilege-separated SSH:/var/empty:/bin/false
EOF
      cat > /etc/group <<'EOF'
root:x:0:
users:x:100:
sshd:x:74:
EOF
      cat > /home/${cfg.user}/.ssh/authorized_keys <<'EOF'
${authorizedKeys}
EOF
      chmod 0600 /home/${cfg.user}/.ssh/authorized_keys
      chown -R 1000:100 /home/${cfg.user}

      echo "tiny-sandbox: generating ephemeral OpenSSH host keys"
      ${extraUtils}/bin/ssh-keygen -q -t ed25519 -N "" -f /etc/ssh/ssh_host_ed25519_key
      ${extraUtils}/bin/ssh-keygen -q -t rsa -b 3072 -N "" -f /etc/ssh/ssh_host_rsa_key
      echo "tiny-sandbox: loading virtio modules"
      modprobe virtio || true
      modprobe virtio_ring || true
      modprobe virtio_mmio || true
      modprobe virtio_pci || true
      modprobe virtio_console || true
      modprobe fuse || true
      modprobe virtiofs || true
      modprobe vsock || true
      modprobe vmw_vsock_virtio_transport_common || true
      modprobe vmw_vsock_virtio_transport || true

      echo "tiny-sandbox: starting QEMU guest agent"
      mkdir -p /dev/virtio-ports
      qga_port=""
      for attempt in $(seq 1 50); do
        for name_file in /sys/class/virtio-ports/*/name; do
          [ -e "$name_file" ] || continue
          if [ "$(cat "$name_file")" = "org.qemu.guest_agent.0" ]; then
            port_name="$(basename "$(dirname "$name_file")")"
            qga_port="/dev/$port_name"
            ln -sf "$qga_port" /dev/virtio-ports/org.qemu.guest_agent.0
            break
          fi
        done
        [ -n "$qga_port" ] && [ -e "$qga_port" ] && break
        sleep 0.1
      done
      ${extraUtils}/bin/qemu-ga -m virtio-serial -p /dev/virtio-ports/org.qemu.guest_agent.0 -t /run -r &

      ${
        lib.optionalString cfg.mountWorkspace ''
          echo "tiny-sandbox: mounting workspace virtiofs"
          mkdir -p ${cfg.workspaceMountPoint}
          mount -t virtiofs workspace ${cfg.workspaceMountPoint}
          chown ${cfg.user}:users ${cfg.workspaceMountPoint} || true
        ''
      }

      # OpenSSH is intentionally used for the first experiment. Dropbear is the
      # expected future size reduction path, but changes the daemon behavior.
      cat > /etc/ssh/sshd_config <<'EOF'
HostKey /etc/ssh/ssh_host_ed25519_key
HostKey /etc/ssh/ssh_host_rsa_key
AuthorizedKeysFile .ssh/authorized_keys
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
PermitUserEnvironment no
PidFile /run/sshd/sshd.pid
PrintMotd no
UsePAM no
Subsystem sftp ${extraUtils}/bin/sftp-server
EOF

      echo "tiny-sandbox: listening for OpenSSH on vsock port 22"
      ${extraUtils}/bin/socat1 VSOCK-LISTEN:22,reuseaddr,fork EXEC:"${extraUtils}/bin/sshd -i -e -f /etc/ssh/sshd_config" &
      echo "tiny-sandbox: appliance ready"
      while true; do
        sleep 3600
      done
    '';

    microvm = {
      guest.enable = false;
      hypervisor = "qemu";
      mem = cfg.machine.memory;
      vcpu = lib.mkIf (cfg.machine.vcpu != null) cfg.machine.vcpu;
      socket = "qmp.sock";
      shares = lib.optionals cfg.mountWorkspace [
        {
          proto = "virtiofs";
          tag = "workspace";
          source = ".";
          mountPoint = cfg.workspaceMountPoint;
          securityModel = "mapped";
        }
      ];
      volumes = [ ];
      storeOnDisk = false;
      writableStoreOverlay = null;

      qemu.serialConsole = true;
      virtiofsd.group = lib.mkDefault null;

      interfaces = [
        {
          type = "user";
          id = "microvm1";
          mac = "02:02:00:00:00:01";
        }
      ];
    };
  };
}
