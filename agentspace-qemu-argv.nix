{
  config,
  lib,
  pkgs,
}:

let
  inherit (pkgs.stdenv.hostPlatform) system;
  microvm = config.microvm;

  arch = builtins.head (builtins.split "-" system);

  accel =
    if pkgs.stdenv.hostPlatform.isLinux then
      "kvm:tcg"
    else if pkgs.stdenv.hostPlatform.isDarwin then
      "hvf:tcg"
    else
      "tcg";

  requirePci = (!lib.hasPrefix "microvm" microvm.qemu.machine) || microvm.shares != [ ];
  devType = if requirePci then "pci" else "device";

  machineOpts =
    if microvm.qemu.machineOpts != null then
      microvm.qemu.machineOpts
    else
      {
        x86_64-linux =
          {
            inherit accel;
            mem-merge = "on";
            acpi = "on";
          }
          // lib.optionalAttrs (microvm.qemu.machine == "microvm") {
            pit = "off";
            pic = "off";
            pcie = if requirePci then "on" else "off";
            rtc = "on";
            usb = "off";
          };
        aarch64-linux = {
          inherit accel;
          gic-version = "max";
        };
        aarch64-darwin = {
          inherit accel;
        };
      }
      .${system};

  machineConfig = builtins.concatStringsSep "," (
    [ microvm.qemu.machine ]
    ++ map (name: "${name}=${machineOpts.${name}}") (builtins.attrNames machineOpts)
  );

  cpuArg =
    if microvm.cpu != null then
      microvm.cpu
    else if system == "x86_64-linux" then
      "host,+x2apic,-sgx"
    else
      "host";

  kernelConsole =
    if !microvm.qemu.serialConsole then
      ""
    else if system == "x86_64-linux" then
      "earlyprintk=ttyS0 console=ttyS0"
    else if system == "aarch64-linux" then
      "console=ttyAMA0"
    else
      "";

  kernelPath = "${microvm.kernel.out}/${pkgs.stdenv.hostPlatform.linux-kernel.target}";

  aioEngine =
    if pkgs.stdenv.hostPlatform.isLinux then
      "io_uring"
    else
      "threads";

  canSandbox =
    builtins.elem "--enable-seccomp" (microvm.qemu.package.configureFlags or [ ]);

  withDriveLetters =
    {
      volumes,
      storeOnDisk,
      ...
    }:
    let
      offset = if storeOnDisk then 1 else 0;
    in
    map ({ fst, snd }: fst // { letter = snd; }) (
      lib.zipLists volumes (lib.drop offset lib.strings.lowerChars)
    );

  volumes = withDriveLetters microvm;

  forwardPortArgs = lib.concatMapStrings (
    {
      proto,
      from,
      host,
      guest,
    }:
    {
      host = "hostfwd=${proto}:${host.address}:${toString host.port}-${guest.address}:${toString guest.port},";
      guest =
        "guestfwd=${proto}:${guest.address}:${toString guest.port}-"
        + "cmd:${microvm.vmHostPackages.netcat}/bin/nc ${host.address} ${toString host.port},";
    }
    .${from}
  ) microvm.forwardPorts;

  shareArgs =
    lib.optionals (microvm.shares != [ ]) (
      lib.optionals pkgs.stdenv.hostPlatform.isLinux [
        "-numa"
        "node,memdev=mem"
        "-object"
        "memory-backend-memfd,id=mem,size=${toString microvm.mem}M,share=on"
      ]
      ++ lib.concatLists (
        lib.imap0 (
          index: share:
          [
            "-chardev"
            "socket,id=fs${toString index},path=${share.socket}"
            "-device"
            "vhost-user-fs-${devType},chardev=fs${toString index},tag=${share.tag}"
          ]
        ) microvm.shares
      )
    );

  interfaceArgs = lib.concatMap (
    interface:
    [
      "-netdev"
      (
        lib.concatStringsSep "," (
          [
            interface.type
            "id=${interface.id}"
          ]
          ++ lib.optionals (microvm.forwardPorts != [ ]) [ forwardPortArgs ]
        )
      )
      "-device"
      (
        "virtio-net-${devType},netdev=${interface.id},mac=${interface.mac}"
        + lib.optionalString
          (requirePci || (microvm.cpu == null && system != "x86_64-linux"))
          ",romfile="
        + lib.optionalString
          (microvm.vcpu > 1 && requirePci)
          ",mq=on,vectors=${toString (2 * microvm.vcpu + 2)}"
      )
    ]
  ) microvm.interfaces;

  volumeArgs = lib.concatMap (
    volume:
    [
      "-drive"
      (
        "id=vd${volume.letter},format=raw,file=${volume.image},if=none,"
        + "aio=${aioEngine},discard=unmap"
        + lib.optionalString (volume.direct != null) ",cache=none"
        + ",read-only=${if volume.readOnly then "on" else "off"}"
      )
      "-device"
      (
        "virtio-blk-${devType},drive=vd${volume.letter}"
        + lib.optionalString (volume.serial != null) ",serial=${volume.serial}"
      )
    ]
  ) volumes;
in
[
  "${microvm.qemu.package}/bin/qemu-system-${arch}"
  "-name"
  config.networking.hostName
  "-M"
  machineConfig
  "-m"
  (toString microvm.mem)
  "-smp"
  (toString microvm.vcpu)
  "-nodefaults"
  "-no-user-config"
  "-no-reboot"
  "-kernel"
  kernelPath
  "-initrd"
  microvm.initrdPath
  "-chardev"
  "stdio,id=stdio,signal=off"
  "-device"
  "virtio-rng-${devType}"
]
++ lib.optionals (microvm.machineId != null) [
  "-smbios"
  "type=1,uuid=${microvm.machineId}"
]
++ lib.optionals microvm.qemu.serialConsole [
  "-serial"
  "chardev:stdio"
]
++ lib.optionals (pkgs.stdenv.hostPlatform.isLinux && microvm.cpu == null) [
  "-enable-kvm"
]
++ [
  "-cpu"
  cpuArg
]
++ lib.optionals (system == "x86_64-linux" || system == "aarch64-linux") [
  "-append"
  "${kernelConsole} reboot=t panic=-1 ${toString microvm.kernelParams}"
]
++ lib.optionals (system == "x86_64-linux") [
  "-device"
  "i8042"
]
++ [ "-nographic" ]
++ lib.optionals canSandbox [
  "-sandbox"
  "on"
]
++ lib.optionals (microvm.user != null) [
  "-user"
  microvm.user
]
++ lib.optionals (microvm.socket != null) [
  "-qmp"
  "unix:${microvm.socket},server,nowait"
]
++ lib.optionals microvm.balloon [
  "-device"
  (
    "virtio-balloon,free-page-reporting=on,id=balloon0"
    + lib.optionalString microvm.deflateOnOOM ",deflate-on-oom=on"
  )
]
++ shareArgs
++ volumeArgs
++ interfaceArgs
++ [
  "-device"
  "vhost-vsock-${devType},guest-cid={{VSOCK_CID}}"
]
++ microvm.qemu.extraArgs
