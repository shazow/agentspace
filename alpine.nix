{
  config,
  lib,
  pkgs,
  sshBaseArgv,
  notificationManifest,
}:

let
  cfg = config.agentspace.sandbox;

  arch = builtins.head (builtins.split "-" pkgs.stdenv.hostPlatform.system);
  rootDisk = cfg.alpine.rootDiskBuilder {
    inherit
      config
      lib
      notificationManifest
      pkgs
      sshBaseArgv
      ;
  };

  transport = "pci";
  virtioFSTransport = "pci";
  aioEngine = if pkgs.stdenv.hostPlatform.isLinux then "io_uring" else "threads";
  defaultMachineOpts = {
    accel =
      if pkgs.stdenv.hostPlatform.isLinux then
        "kvm:tcg"
      else if pkgs.stdenv.hostPlatform.isDarwin then
        "hvf:tcg"
      else
        "tcg";
    mem-merge = "on";
    acpi = "on";
    pit = "off";
    pic = "off";
    pcie = "on";
    rtc = "on";
    usb = "off";
  };
  machineOpts =
    if config.microvm.qemu.machineOpts != null then
      config.microvm.qemu.machineOpts
    else
      defaultMachineOpts;
  cpuModel =
    if config.microvm.cpu != null then
      config.microvm.cpu
    else if pkgs.stdenv.hostPlatform.isLinux then
      "host,+x2apic,-sgx"
    else
      "host";

  rootDiskPath = "${cfg.persistence.basedir}/alpine-root.img";

  workspaceVirtioFS = lib.optionals cfg.mountWorkspace [
    {
      id = "fs0";
      socketPath = "workspace.sock";
      tag = "workspace";
      transport = virtioFSTransport;
    }
  ];

  workspaceVirtioFSDaemons = lib.optionals cfg.mountWorkspace [
    {
      tag = "workspace";
      socketPath = "workspace.sock";
      command = {
        path = pkgs.writeShellScript "virtiofsd-${cfg.hostName}-workspace" ''
          socket_path=''${VIRTIE_SOCKET_PATH:-workspace.sock}
          exec ${lib.getExe config.microvm.virtiofsd.package} \
            --socket-path="$socket_path" \
            --shared-dir="$REPO_DIR" \
            --thread-pool-size ${toString config.microvm.virtiofsd.threadPoolSize} \
            --posix-acl --xattr \
            --cache=auto
        '';
        args = [ ];
      };
    }
  ];

  qemuConfig = {
    binaryPath = "${pkgs.qemu}/bin/qemu-system-${arch}";
    name = cfg.hostName;
    machine = {
      type = "microvm";
      options = map (name: "${name}=${machineOpts.${name}}") (builtins.attrNames machineOpts);
    };
    cpu = {
      model = cpuModel;
      enableKvm = pkgs.stdenv.hostPlatform.isLinux && config.microvm.cpu == null;
    };
    memory = {
      sizeMiB = config.microvm.mem;
      backend = if cfg.mountWorkspace && pkgs.stdenv.hostPlatform.isLinux then "memfd" else "default";
      shared = cfg.mountWorkspace;
    };
    kernel = {
      path = "${rootDisk}/vmlinuz-virt";
      initrdPath = "${rootDisk}/initramfs-virt";
      params = lib.concatStringsSep " " [
        "console=ttyS0"
        "reboot=t"
        "panic=-1"
        "root=/dev/vda"
        "rw"
        "rootfstype=ext4"
        "modules=virtio_pci,virtio_blk,virtio_net,virtio_console,virtio_rng,virtiofs"
        "rootwait"
        "quiet"
      ];
    };
    smp.cpus = config.microvm.vcpu;
    console = {
      stdioChardev = true;
      serialConsole = true;
    };
    knobs = {
      noDefaults = true;
      noUserConfig = true;
      noReboot = true;
      noGraphic = true;
      seccompSandbox = builtins.elem "--enable-seccomp" (
        config.microvm.qemu.package.configureFlags or [ ]
      );
    };
    qmp.socketPath = "qmp.sock";
    guestAgent.socketPath = "qga.sock";
    devices = {
      rng = {
        id = "rng0";
        inherit transport;
      };
      i8042 = pkgs.stdenv.hostPlatform.system == "x86_64-linux";
      virtiofs = workspaceVirtioFS;
      block = [
        {
          id = "vda";
          imagePath = rootDiskPath;
          aio = aioEngine;
          cache = null;
          readOnly = false;
          serial = "agentspace-alpine-root";
          inherit transport;
        }
      ];
      network = [
        {
          id = "microvm1";
          backend = "user";
          macAddress = "02:02:00:00:00:01";
          romFile = "";
          netdevOptions = [ ];
          mqVectors = if config.microvm.vcpu > 1 then 2 * config.microvm.vcpu + 2 else 0;
          inherit transport;
        }
      ];
      vsock = {
        id = "vsock0";
        inherit transport;
      };
    }
    // lib.optionalAttrs config.microvm.balloon {
      balloon = {
        id = "balloon0";
        inherit transport;
        deflateOnOOM = config.microvm.deflateOnOOM;
        freePageReporting = true;
      };
    };
    passthroughArgs = config.microvm.qemu.extraArgs;
  };

  commonInit = ''
    echo "Preparing experimental Alpine Agent QEMU Sandbox..."
    ${pkgs.coreutils}/bin/mkdir -p ${lib.escapeShellArg cfg.persistence.basedir}
    if [ ! -e ${lib.escapeShellArg rootDiskPath} ]; then
      echo "Installing Alpine root disk at ${rootDiskPath}"
      ${pkgs.coreutils}/bin/install -m 0644 ${lib.escapeShellArg "${rootDisk}/alpine-root.img"} ${lib.escapeShellArg rootDiskPath}
      ${pkgs.coreutils}/bin/chmod u+w ${lib.escapeShellArg rootDiskPath}
    fi
  ''
  + lib.optionalString cfg.mountWorkspace ''
    echo "Mounting current directory at ~/workspace"
    cd "$REPO_DIR"
  '';

  virtieManifestData = {
    identity.hostName = cfg.hostName;
    paths = {
      workingDir = ".";
      lockPath = "/tmp/agentspace-${cfg.hostName}.lock";
      runtimeDir = "";
    };
    persistence = {
      directories = [ cfg.persistence.basedir ];
      baseDir = cfg.persistence.basedir;
      stateDir = cfg.persistence.basedir;
    };
    ssh = {
      argv = sshBaseArgv;
      user = cfg.user;
    };
    qemu = qemuConfig;
    volumes = [ ];
    virtiofs.daemons = workspaceVirtioFSDaemons;
    writeFiles = cfg.writeFiles;
    notifications = notificationManifest;
  };

  virtieManifestTemplate = pkgs.writeText "virtie-${cfg.hostName}.json" (
    builtins.toJSON virtieManifestData
  );
in
{
  inherit
    commonInit
    rootDisk
    virtieManifestData
    virtieManifestTemplate
    ;
  virtieManifest = "${cfg.persistence.basedir}/virtie-${cfg.hostName}.json";
}
