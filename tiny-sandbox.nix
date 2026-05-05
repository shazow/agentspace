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
    guestAgent.socketPath = "";
  };
  extraUtils = config.system.build.extraUtils;
  opensshLibexecCompat = "/nix/store/eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee-openssh-${pkgs.openssh.version}/libexec";

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
      user = "agent";
    };
    qemu = qemuConfig;
    volumes = [ ];
    virtiofs.daemons = [ ];
    writeFiles = { };
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

    memoryMiB = lib.mkOption {
      type = lib.types.ints.positive;
      default = 256;
      description = "Memory size for the tiny guest in MiB.";
    };

    vcpu = lib.mkOption {
      type = lib.types.ints.positive;
      default = 1;
      description = "Number of vCPUs for the tiny guest.";
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

      mkdir -p /etc/ssh /run/sshd /var/empty /home/agent/.ssh /tmp
      mkdir -p ${opensshLibexecCompat}
      ln -sf ${extraUtils}/bin/sshd-auth ${opensshLibexecCompat}/sshd-auth
      ln -sf ${extraUtils}/bin/sshd-session ${opensshLibexecCompat}/sshd-session
      chmod 0755 /run/sshd /var/empty
      chmod 0700 /home/agent /home/agent/.ssh

      cat > /etc/passwd <<'EOF'
root:x:0:0:root:/root:/bin/sh
agent:x:1000:100:Agent:/home/agent:/bin/sh
sshd:x:74:74:Privilege-separated SSH:/var/empty:/bin/false
EOF
      cat > /etc/group <<'EOF'
root:x:0:
users:x:100:
sshd:x:74:
EOF
      cat > /home/agent/.ssh/authorized_keys <<'EOF'
${authorizedKeys}
EOF
      chmod 0600 /home/agent/.ssh/authorized_keys
      chown -R 1000:100 /home/agent

      echo "tiny-sandbox: generating ephemeral OpenSSH host keys"
      ${extraUtils}/bin/ssh-keygen -q -t ed25519 -N "" -f /etc/ssh/ssh_host_ed25519_key
      ${extraUtils}/bin/ssh-keygen -q -t rsa -b 3072 -N "" -f /etc/ssh/ssh_host_rsa_key
      echo "tiny-sandbox: loading virtio-vsock modules"
      modprobe virtio || true
      modprobe virtio_ring || true
      modprobe virtio_mmio || true
      modprobe vsock || true
      modprobe vmw_vsock_virtio_transport_common || true
      modprobe vmw_vsock_virtio_transport || true

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
      mem = cfg.memoryMiB;
      vcpu = cfg.vcpu;
      socket = "qmp.sock";
      shares = [ ];
      volumes = [ ];
      storeOnDisk = false;
      writableStoreOverlay = null;

      qemu.serialConsole = true;

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
