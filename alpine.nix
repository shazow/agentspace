{
  config,
  lib,
  pkgs,
  sshBaseArgv,
  notificationManifest,
}:

let
  cfg = config.agentspace.sandbox;

  alpineRelease = "v3.22";
  apkoArch = "amd64";
  alpineMirror = "https://dl-cdn.alpinelinux.org/alpine";
  alpineRepositories = [
    "${alpineMirror}/${alpineRelease}/main"
    "${alpineMirror}/${alpineRelease}/community"
  ];

  arch = builtins.head (builtins.split "-" pkgs.stdenv.hostPlatform.system);

  transport = "pci";
  virtioFSTransport = "pci";
  aioEngine = if pkgs.stdenv.hostPlatform.isLinux then "io_uring" else "threads";
  accel =
    if pkgs.stdenv.hostPlatform.isLinux then
      "kvm:tcg"
    else if pkgs.stdenv.hostPlatform.isDarwin then
      "hvf:tcg"
    else
      "tcg";
  cpuModel = if pkgs.stdenv.hostPlatform.isLinux then "host,+x2apic,-sgx" else "host";

  authorizedKeys = lib.concatStringsSep "\n" cfg.ssh.authorizedKeys;

  apkoConfig = pkgs.writeText "agentspace-alpine.apko.yaml" ''
    contents:
      repositories:
    ${lib.concatMapStringsSep "\n" (repo: "    - ${repo}") alpineRepositories}
      packages:
        - alpine-base
        - busybox-extras
        - ca-certificates
        - curl
        - e2fsprogs-extra
        - git
        - iproute2
        - less
        - linux-virt
        - nano
        - openssh
        - qemu-guest-agent
        - socat
        - sudo
        - util-linux

    environment:
      PATH: /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

    archs:
      - ${apkoArch}
  '';

  initScript = pkgs.writeText "agentspace-init" ''
    #!/sbin/openrc-run

    description="Prepare agentspace guest compatibility paths and mounts"

    depend() {
      need localmount
      before qemu-guest-agent sshd agentspace-vsock-ssh
    }

    start() {
      ebegin "Preparing agentspace guest"
      install -d /run/current-system/sw/bin
      for tool in chmod chown install test; do
        ln -sf "/bin/$tool" "/run/current-system/sw/bin/$tool"
      done
      ${lib.optionalString cfg.mountWorkspace ''
        install -d -o ${cfg.user} -g users ${lib.escapeShellArg cfg.workspaceMountPoint}
        mountpoint -q ${lib.escapeShellArg cfg.workspaceMountPoint} || mount -t virtiofs workspace ${lib.escapeShellArg cfg.workspaceMountPoint}
      ''}
      eend $?
    }
  '';

  vsockSSHScript = pkgs.writeText "agentspace-vsock-ssh" ''
    #!/sbin/openrc-run

    description="Forward guest vsock port 22 to local sshd"
    command="/usr/bin/socat"
    command_args="VSOCK-LISTEN:22,fork,reuseaddr TCP:127.0.0.1:22"
    command_background="yes"
    pidfile="/run/agentspace-vsock-ssh.pid"

    depend() {
      need sshd
    }
  '';

  rootfs = pkgs.stdenvNoCC.mkDerivation {
    pname = "agentspace-alpine-rootfs";
    version = alpineRelease;
    nativeBuildInputs = [
      pkgs.apko
      pkgs.fakeroot
      pkgs.gnutar
      pkgs.gzip
    ];
    outputHashMode = "recursive";
    outputHash = "sha256-OipzzXth6czd4AxNi58EwlKYVKshrP8ACK6k1Z4Zafk=";

    buildCommand = ''
      export HOME="$TMPDIR"
      export SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt
      apko build-minirootfs --build-arch ${apkoArch} ${apkoConfig} rootfs.tar

      mkdir root
      tar --extract --file rootfs.tar --directory root --no-same-owner --no-same-permissions \
        --exclude='dev/*' --exclude='./dev/*' \
        --exclude='bin/bbsuid' --exclude='./bin/bbsuid'
      mkdir -p root/dev

      mkdir -p "$out"
      fakeroot tar --create --numeric-owner --file "$out/rootfs.tar" --directory root .
    '';
  };

  rootDisk = pkgs.stdenvNoCC.mkDerivation {
    pname = "agentspace-alpine-root";
    version = alpineRelease;
    nativeBuildInputs = [
      pkgs.cpio
      pkgs.e2fsprogs
      pkgs.fakeroot
      pkgs.gnutar
    ];

    buildCommand = ''
      mkdir root
      fakeroot tar --extract --file ${rootfs}/rootfs.tar --directory root --numeric-owner

      install -Dm0755 ${initScript} root/etc/init.d/agentspace-init
      install -Dm0755 ${vsockSSHScript} root/etc/init.d/agentspace-vsock-ssh
      ln -s /etc/init.d/agentspace-init root/etc/runlevels/boot/agentspace-init
      ln -s /etc/init.d/devfs root/etc/runlevels/boot/devfs
      ln -s /etc/init.d/procfs root/etc/runlevels/boot/procfs
      ln -s /etc/init.d/sysfs root/etc/runlevels/boot/sysfs
      ln -s /etc/init.d/qemu-guest-agent root/etc/runlevels/default/qemu-guest-agent
      ln -s /etc/init.d/sshd root/etc/runlevels/default/sshd
      ln -s /etc/init.d/agentspace-vsock-ssh root/etc/runlevels/default/agentspace-vsock-ssh

      install -d -m 0755 root/home/${cfg.user}
      install -d -m 0700 root/home/${cfg.user}/.ssh
      install -d root/run/current-system/sw/bin
      cat > authorized_keys <<'KEYS'
      ${authorizedKeys}
      KEYS
      install -m 0600 authorized_keys root/home/${cfg.user}/.ssh/authorized_keys

      if ! grep -q '^${cfg.user}:' root/etc/passwd; then
        echo '${cfg.user}:x:1000:100:agentspace user:/home/${cfg.user}:/bin/ash' >> root/etc/passwd
      fi
      if ! grep -q '^${cfg.user}:' root/etc/shadow; then
        echo '${cfg.user}:!:19000:0:99999:7:::' >> root/etc/shadow
      fi
      sed -i 's/^wheel:x:10:.*/wheel:x:10:${cfg.user}/' root/etc/group
      echo '%wheel ALL=(ALL) NOPASSWD: ALL' > root/etc/sudoers.d/agentspace
      chmod 0440 root/etc/sudoers.d/agentspace

      echo '${cfg.hostName}' > root/etc/hostname
      cat > root/etc/hosts <<'HOSTS'
      127.0.0.1 localhost
      127.0.1.1 ${cfg.hostName}
      ::1 localhost
      HOSTS

      mkdir -p root/etc/ssh/sshd_config.d
      cat > root/etc/ssh/sshd_config.d/agentspace.conf <<'SSHD'
      PasswordAuthentication no
      KbdInteractiveAuthentication no
      PermitRootLogin no
      AllowUsers ${cfg.user}
      SSHD

      ${lib.optionalString cfg.mountWorkspace ''
        mkdir -p root${cfg.workspaceMountPoint}
      ''}

      kernel_version="$(cd root/lib/modules && echo *)"
      initrd="initrd"
      mkdir -p \
        "$initrd/bin" \
        "$initrd/sbin" \
        "$initrd/dev" \
        "$initrd/proc" \
        "$initrd/sys" \
        "$initrd/tmp" \
        "$initrd/newroot" \
        "$initrd/lib/modules/$kernel_version"
      cp root/bin/busybox "$initrd/bin/busybox"
      for applet in sh mount mkdir mdev modprobe insmod gzip uname switch_root sleep echo; do
        ln -s /bin/busybox "$initrd/bin/$applet"
      done
      ln -s /bin/busybox "$initrd/sbin/modprobe"
      cp root/lib/ld-musl-*.so.1 "$initrd/lib/"
      cp root/lib/modules/"$kernel_version"/modules.{alias,builtin,dep} "$initrd/lib/modules/$kernel_version/"
      for module in \
        kernel/drivers/block/virtio_blk.ko.gz \
        kernel/fs/ext4/ext4.ko.gz \
        kernel/fs/jbd2/jbd2.ko.gz \
        kernel/fs/mbcache.ko.gz \
        kernel/lib/crc16.ko.gz; do
        install -Dm0644 "root/lib/modules/$kernel_version/$module" "$initrd/lib/modules/$kernel_version/$module"
      done
      cat > "$initrd/init" <<'INIT'
      #!/bin/sh
      mount -t proc proc /proc
      mount -t sysfs sysfs /sys
      mount -t devtmpfs devtmpfs /dev || {
        mount -t tmpfs tmpfs /dev
        mdev -s
      }
      k="$(uname -r)"
      load_module() {
        src="/lib/modules/$k/$1"
        name="''${1##*/}"
        dst="/tmp/''${name%.gz}"
        gzip -dc "$src" > "$dst"
        insmod "$dst" || true
      }
      load_module kernel/drivers/block/virtio_blk.ko.gz
      load_module kernel/lib/crc16.ko.gz
      load_module kernel/fs/mbcache.ko.gz
      load_module kernel/fs/jbd2/jbd2.ko.gz
      load_module kernel/fs/ext4/ext4.ko.gz
      rootdev=
      for _ in 1 2 3 4 5 6 7 8 9 10; do
        for candidate in /dev/vda /dev/vdb /dev/sda /dev/sdb; do
          if [ -b "$candidate" ]; then
            rootdev="$candidate"
            break 2
          fi
        done
        sleep 1
      done
      mount "$rootdev" /newroot
      exec switch_root /newroot /sbin/init
      INIT
      chmod 0755 "$initrd/init"
      (cd "$initrd" && find . -print0 | cpio --null -o --format=newc | gzip -9n > ../initramfs-virt)

      fakeroot_state="$TMPDIR/agentspace-alpine.fakeroot"
      fakeroot -s "$fakeroot_state" chown -R 1000:100 root/home/${cfg.user}

      size="$((1024 * 1024 * 1024))"
      truncate -s "$size" alpine-root.img
      fakeroot -i "$fakeroot_state" mkfs.ext4 -q -L AGENTROOT -d root alpine-root.img
      mkdir -p "$out"
      cp alpine-root.img "$out/alpine-root.img"
      cp root/boot/vmlinuz-virt "$out/vmlinuz-virt"
      cp initramfs-virt "$out/initramfs-virt"
    '';
  };

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
      options = [
        "accel=${accel}"
        "mem-merge=on"
        "acpi=on"
        "pit=off"
        "pic=off"
        "pcie=on"
        "rtc=on"
        "usb=off"
      ];
    };
    cpu = {
      model = cpuModel;
      enableKvm = pkgs.stdenv.hostPlatform.isLinux;
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
