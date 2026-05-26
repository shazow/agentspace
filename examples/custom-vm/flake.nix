{
  description = "Standalone virtie manifest example using a reusable custom VM image";

  inputs = {
    agentspace.url = "path:../..";
    nixpkgs.follows = "agentspace/nixpkgs";
    gondolin-alpine = {
      url = "https://github.com/earendil-works/gondolin/releases/download/image-alpine-base--0.1.1/gondolin-image-alpine-base-0.1.1-x86_64.tar.gz";
      flake = false;
    };
  };

  outputs =
    {
      agentspace,
      nixpkgs,
      gondolin-alpine,
      ...
    }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};

      manifest = pkgs.writeText "manifest.toml" ''
        host_name = "custom-vm"
        state_dir = ".virtie"

        [qemu]
        exec = [
          "${pkgs.qemu_kvm}/bin/qemu-system-x86_64",
          "-chardev", "socket,path=.virtie/virtio-port.sock,server=on,wait=off,id=virtio_port_char",
          "-device", "virtserialport,chardev=virtio_port_char,name=virtio-port",
          "-chardev", "socket,path=.virtie/virtio-fs.sock,server=on,wait=off,id=virtio_fs_char",
          "-device", "virtserialport,chardev=virtio_fs_char,name=virtio-fs",
        ]

        [machine]
        memory = 512
        vcpu = 1

        [kernel]
        path = "${gondolin-alpine}/vmlinuz-virt"
        initrd_path = "${gondolin-alpine}/initramfs.cpio.lz4"
        params = [ "root=/dev/vda", "rw" ]
        serial = "console"

        [ssh]
        ready_socket = ""

        [[volumes]]
        image = ".virtie/rootfs.ext4"
        fs = "ext4"
        create = false
        label = "gondolin-root"
      '';

      launch = pkgs.writeShellScriptBin "launch-custom-vm" ''
        set -euo pipefail

        mkdir -p .virtie
        if [ ! -e .virtie/rootfs.ext4 ]; then
          cp ${gondolin-alpine}/rootfs.ext4 .virtie/rootfs.ext4
          chmod u+w .virtie/rootfs.ext4
        fi
        install -m 0644 ${manifest} manifest.toml

        exec ${
          agentspace.packages.${system}.virtie
        }/bin/virtie launch -v --resume=no --manifest=manifest.toml "$@"
      '';
    in
    {
      packages.${system} = {
        default = manifest;
        manifest = manifest;
      };

      apps.${system} = {
        default = {
          type = "app";
          program = "${launch}/bin/launch-custom-vm";
        };
      };
    };
}
