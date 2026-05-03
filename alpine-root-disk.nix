{
  alpineRelease ? "v3.22",
  apkoArch ? "amd64",
  alpineMirror ? "https://dl-cdn.alpinelinux.org/alpine",
  repositories ? [
    "${alpineMirror}/${alpineRelease}/main"
    "${alpineMirror}/${alpineRelease}/community"
  ],
  packages ? null,
  extraPackages ? [ ],
  rootfsOutputHash ? "sha256-gelXx81rvXpb2z+5w3yltm+BQuPAsrHL3uDwAsqGVlw=",
  rootDiskSizeMiB ? 512,
  pname ? "agentspace-alpine-root",
  rootfsPname ? "agentspace-alpine-rootfs",
}:

{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.agentspace.sandbox;

  basePackages = [
    "alpine-base"
    "busybox-extras"
    "ca-certificates"
    "curl"
    "e2fsprogs-extra"
    "git"
    "iproute2"
    "less"
    "linux-virt"
    "nano"
    "openssh"
    "qemu-guest-agent"
    "socat"
    "sudo"
    "util-linux"
  ];
  rootfsPackages = if packages == null then basePackages ++ extraPackages else packages;
  authorizedKeys = lib.concatStringsSep "\n" cfg.ssh.authorizedKeys;

  apkoConfig = pkgs.writeText "agentspace-alpine.apko.yaml" ''
    contents:
      repositories:
    ${lib.concatMapStringsSep "\n" (repo: "    - ${repo}") repositories}
      packages:
    ${lib.concatMapStringsSep "\n" (package: "    - ${package}") rootfsPackages}

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

  sshdScript = pkgs.writeText "sshd" ''
    #!/sbin/openrc-run

    description="OpenSSH server"
    command="/usr/sbin/sshd"
    command_args="-D -e"
    command_background="yes"
    pidfile="/run/sshd.pid"

    depend() {
      need net
      after agentspace-init
    }

    start_pre() {
      install -d -m 0755 /run/sshd
      ssh-keygen -A
    }
  '';

  qemuGuestAgentScript = pkgs.writeText "qemu-guest-agent" ''
    #!/sbin/openrc-run

    description="QEMU guest agent"
    command="/usr/bin/qemu-ga"
    command_args="-m virtio-serial -p /dev/virtio-ports/org.qemu.guest_agent.0"
    command_background="yes"
    pidfile="/run/qemu-guest-agent.pid"

    depend() {
      need dev
      after agentspace-init
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

    start_pre() {
      modprobe vsock
      modprobe vmw_vsock_virtio_transport
    }
  '';

  rootfs = pkgs.stdenvNoCC.mkDerivation {
    pname = rootfsPname;
    version = alpineRelease;
    nativeBuildInputs = [
      pkgs.apko
      pkgs.fakeroot
      pkgs.gnutar
      pkgs.gzip
    ];
    outputHashMode = "recursive";
    outputHash = rootfsOutputHash;

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
      fakeroot tar \
        --create \
        --sort=name \
        --mtime='@1' \
        --owner=0 \
        --group=0 \
        --numeric-owner \
        --file "$out/rootfs.tar" \
        --directory root .
    '';
  };
in
pkgs.stdenvNoCC.mkDerivation {
  inherit pname;
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
    install -Dm0755 ${sshdScript} root/etc/init.d/sshd
    install -Dm0755 ${qemuGuestAgentScript} root/etc/init.d/qemu-guest-agent
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
      echo '${cfg.user}:*:19000:0:99999:7:::' >> root/etc/shadow
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
    cat > root/etc/network/interfaces <<'INTERFACES'
    auto lo
    iface lo inet loopback

    auto eth0
    iface eth0 inet dhcp
    INTERFACES

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
      kernel/drivers/net/net_failover.ko.gz \
      kernel/drivers/net/virtio_net.ko.gz \
      kernel/net/core/failover.ko.gz \
      kernel/crypto/crc32c_generic.ko.gz \
      kernel/lib/libcrc32c.ko.gz \
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
      insmod "$dst"
    }
    load_module kernel/drivers/block/virtio_blk.ko.gz
    load_module kernel/net/core/failover.ko.gz
    load_module kernel/drivers/net/net_failover.ko.gz
    load_module kernel/drivers/net/virtio_net.ko.gz
    load_module kernel/crypto/crc32c_generic.ko.gz
    load_module kernel/lib/libcrc32c.ko.gz
    load_module kernel/lib/crc16.ko.gz
    load_module kernel/fs/mbcache.ko.gz
    load_module kernel/fs/jbd2/jbd2.ko.gz
    load_module kernel/fs/ext4/ext4.ko.gz
    mdev -s
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
    if [ -z "$rootdev" ]; then
      echo "failed to find Alpine root block device" >&2
      exec sh
    fi
    mount -t ext4 "$rootdev" /newroot
    exec switch_root /newroot /sbin/init
    INIT
    chmod 0755 "$initrd/init"
    (cd "$initrd" && find . -print0 | cpio --null -o --format=newc | gzip -9n > ../initramfs-virt)

    fakeroot_state="$TMPDIR/agentspace-alpine.fakeroot"
    fakeroot -s "$fakeroot_state" chown -R 1000:100 root/home/${cfg.user}

    size="$(( ${toString rootDiskSizeMiB} * 1024 * 1024 ))"
    truncate -s "$size" alpine-root.img
    fakeroot -i "$fakeroot_state" mkfs.ext4 -q -L AGENTROOT -d root alpine-root.img
    mkdir -p "$out"
    cp alpine-root.img "$out/alpine-root.img"
    cp root/boot/vmlinuz-virt "$out/vmlinuz-virt"
    cp initramfs-virt "$out/initramfs-virt"
  '';
}
