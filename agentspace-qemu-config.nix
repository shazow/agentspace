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
  transport = if requirePci then "pci" else "mmio";

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

  kernelParams = lib.concatStringsSep " " (
    lib.filter (value: value != "") [
      kernelConsole
      "reboot=t"
      "panic=-1"
      (toString microvm.kernelParams)
    ]
  );

  netdevOptions = lib.concatMap (
    {
      proto,
      from,
      host,
      guest,
    }:
    if from == "host" then
      [ "hostfwd=${proto}:${host.address}:${toString host.port}-${guest.address}:${toString guest.port}" ]
    else
      [
        "guestfwd=${proto}:${guest.address}:${toString guest.port}-cmd:${microvm.vmHostPackages.netcat}/bin/nc ${host.address} ${toString host.port}"
      ]
  ) microvm.forwardPorts;

in
{
  binaryPath = "${microvm.qemu.package}/bin/qemu-system-${arch}";
  name = config.networking.hostName;

  machine = {
    type = microvm.qemu.machine;
    options = map (name: "${name}=${machineOpts.${name}}") (builtins.attrNames machineOpts);
  };

  cpu = {
    model = cpuArg;
    enableKvm = pkgs.stdenv.hostPlatform.isLinux && microvm.cpu == null;
  };

  memory = {
    sizeMiB = microvm.mem;
    backend = if microvm.shares != [ ] && pkgs.stdenv.hostPlatform.isLinux then "memfd" else "default";
    shared = microvm.shares != [ ];
  };

  kernel = {
    path = kernelPath;
    initrdPath = microvm.initrdPath;
    params = kernelParams;
  };

  smp = {
    cpus = microvm.vcpu;
  };

  console = {
    stdioChardev = true;
    serialConsole = microvm.qemu.serialConsole;
  };

  knobs = {
    noDefaults = true;
    noUserConfig = true;
    noReboot = true;
    noGraphic = true;
    seccompSandbox = canSandbox;
  };

  machineId = microvm.machineId;

  qmp = {
    socketPath = if microvm.socket != null then microvm.socket else "qmp.sock";
  };

  devices = {
    rng = {
      id = "rng0";
      inherit transport;
    };

    i8042 = system == "x86_64-linux";

    balloon =
      if microvm.balloon then
        {
          id = "balloon0";
          inherit transport;
          deflateOnOOM = microvm.deflateOnOOM;
          freePageReporting = true;
        }
      else
        null;

    virtiofs = lib.imap0 (
      index: share: {
        id = "fs${toString index}";
        socketPath = share.socket;
        tag = share.tag;
        inherit transport;
      }
    ) microvm.shares;

    block = builtins.map (
      volume: {
        id = "vd${volume.letter}";
        imagePath = volume.image;
        aio = aioEngine;
        cache = if volume.direct != null then "none" else null;
        readOnly = volume.readOnly;
        serial = volume.serial;
        inherit transport;
      }
    ) volumes;

    network = builtins.map (
      interface: {
        id = interface.id;
        backend = interface.type;
        macAddress = interface.mac;
        romFile =
          if requirePci || (microvm.cpu == null && system != "x86_64-linux") then "" else null;
        netdevOptions = netdevOptions;
        mqVectors = if microvm.vcpu > 1 && requirePci then 2 * microvm.vcpu + 2 else 0;
        inherit transport;
      }
    ) microvm.interfaces;

    vsock = {
      id = "vsock0";
      inherit transport;
    };
  };

  passthroughArgs =
    lib.optionals (microvm.user != null) [
      "-user"
      microvm.user
    ]
    ++ microvm.qemu.extraArgs;
}
