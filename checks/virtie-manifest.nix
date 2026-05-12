{
  mkSandbox,
  pkgs,
  ...
}:
let
  testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBIqXkHFLTDd7n09425txXfdOgJDUb7CpMAdCPVRS94z agentspace-virtie-test";
  notificationCommand = "notify-send \"virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE\"";
  fakeQemuPackage = pkgs.runCommand "fake-qemu-package" { configureFlags = [ ]; } ''
    mkdir -p "$out/bin"
    touch "$out/bin/qemu-system-x86_64"
  '';

  vmVirtie = mkSandbox {
    ssh.authorizedKeys = [ testPublicKey ];
    ssh.identityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
  };

  vmVirtieFeatureRich = mkSandbox {
    ssh.authorizedKeys = [ testPublicKey ];
    ssh.identityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    balloon = true;
    writeFiles = {
      "/etc/agentspace-inline" = {
        chown = "agent:users";
        text = "hello";
        mode = "0640";
        overwrite = true;
      };
      "/etc/agentspace-host" = {
        path = ".agentspace-test/host-file";
      };
    };
    notifications = {
      command = notificationCommand;
      states = [
        "runtime:resume"
        "runtime:suspend"
        "balloon:resize"
      ];
    };
  };

  vmVirtieBalloonDisabled = mkSandbox {
    ssh.authorizedKeys = [ testPublicKey ];
    ssh.identityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    balloon = false;
  };

  vmVirtieExternalStoreSocket = mkSandbox {
    ssh.authorizedKeys = [ testPublicKey ];
    ssh.identityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    mountWorkspace = false;
    nixStoreShareSocket = "/var/run/virtiofs-nix-store.sock";
  };

  vmVirtieExtraShares = mkSandbox {
    ssh.authorizedKeys = [ testPublicKey ];
    ssh.identityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    shares = [
      {
        proto = "9p";
        tag = "cache";
        source = ".agentspace-test/cache";
        mountPoint = "/mnt/cache";
        securityModel = "mapped";
        readOnly = true;
      }
      {
        proto = "virtiofs";
        tag = "tools";
        source = ".agentspace-test/tools";
        mountPoint = "/mnt/tools";
        readOnly = true;
      }
    ];
  };

  vmVirtieFixedMachine = mkSandbox {
    ssh.authorizedKeys = [ testPublicKey ];
    ssh.identityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    machine = {
      memory = 512;
      vcpu = 2;
    };
  };

  manifest = vmVirtie.config.agentspace.sandbox.launch.virtieManifestData;
  featureRichManifest = vmVirtieFeatureRich.config.agentspace.sandbox.launch.virtieManifestData;
  disabledBalloonManifest =
    vmVirtieBalloonDisabled.config.agentspace.sandbox.launch.virtieManifestData;
  externalStoreSocketManifest =
    vmVirtieExternalStoreSocket.config.agentspace.sandbox.launch.virtieManifestData;
  extraSharesManifest = vmVirtieExtraShares.config.agentspace.sandbox.launch.virtieManifestData;
  fixedMachineManifest =
    vmVirtieFixedMachine.config.agentspace.sandbox.launch.virtieManifestData;

  graphicalQemuConfig = import ../agentspace-qemu-config.nix {
    inherit pkgs;
    lib = pkgs.lib;
    config = {
      networking.hostName = "graphical-contract";
      microvm = {
        qemu = {
          machine = "microvm";
          machineOpts = {
            accel = "tcg";
            mem-merge = "on";
            acpi = "on";
            pit = "off";
            pic = "off";
            pcie = "on";
            rtc = "on";
            usb = "off";
          };
          package = fakeQemuPackage;
          serialConsole = false;
          extraArgs = [ ];
        };
        graphics = {
          enable = true;
          backend = "gtk";
        };
        shares = [ ];
        cpu = "max";
        kernel = {
          out = pkgs.emptyDirectory;
        };
        initrdPath = "${pkgs.emptyDirectory}/initrd";
        kernelParams = [ ];
        forwardPorts = [ ];
        storeOnDisk = false;
        volumes = [ ];
        vcpu = 1;
        mem = 768;
        socket = "qmp.sock";
        machineId = null;
        user = null;
        interfaces = [
          {
            type = "user";
            id = "net0";
            mac = "02:02:00:00:00:01";
          }
        ];
      };
    };
  };

  _ =
    assert manifest.qemu.binaryPath != "";
    assert manifest.qemu.name == "agent-sandbox";
    assert builtins.length manifest.qemu.machine.options > 0;
    assert manifest.qemu.machine.type == "microvm";
    assert manifest.paths.runtimeDir == "";
    assert manifest.persistence.baseDir == ".agentspace";
    assert manifest.persistence.stateDir == ".agentspace";
    assert manifest.qemu.qmp.socketPath == "qmp.sock";
    assert manifest.qemu.guestAgent.socketPath == "qga.sock";
    assert manifest.qemu.sshReady.socketPath == "ssh-ready.sock";
    assert manifest.qemu.memory.sizeMiB == 4096;
    assert !(manifest.qemu.smp ? cpus);
    assert manifest.qemu.knobs.noGraphic == true;
    assert !(manifest.qemu ? graphics);
    assert manifest.writeFiles == { };
    assert manifest.notifications.states == [ ];
    assert !(manifest.notifications ? command);
    assert manifest.qemu.devices.rng.id == "rng0";
    assert builtins.length manifest.qemu.devices.virtiofs > 0;
    assert manifest.qemu.devices."9p" == [ ];
    assert builtins.length manifest.qemu.devices.block > 0;
    assert builtins.length manifest.qemu.devices.network > 0;
    assert manifest.qemu.devices.vsock.id == "vsock0";
    assert vmVirtie.config.systemd.services.virtie-ssh-signal.after == [ "sshd.service" ];
    assert vmVirtie.config.systemd.services.virtie-ssh-signal.requires == [ "sshd.service" ];
    assert vmVirtie.config.systemd.services.virtie-ssh-signal.serviceConfig.Type == "oneshot";
    assert builtins.head manifest.ssh.argv == "${pkgs.openssh}/bin/ssh";
    assert manifest.ssh.user == "agent";
    assert !(manifest.ssh ? retryDelayMs);
    assert builtins.elem ".agentspace-test/id_ed25519" manifest.ssh.argv;
    assert builtins.length manifest.volumes > 0;
    assert builtins.any (
      volume: volume.imagePath == ".agentspace/nix-store-overlay.img"
    ) manifest.volumes;
    assert builtins.length manifest.virtiofs.daemons > 0;
    assert builtins.all (
      daemon: daemon.socketPath != "" && daemon.command.path != ""
    ) manifest.virtiofs.daemons;
    assert builtins.any (daemon: daemon.tag == "workspace") manifest.virtiofs.daemons;
    assert !(manifest ? vsock);
    assert !(manifest.ssh ? destination);
    assert !(manifest.qemu ? argvTemplate);
    assert !(manifest ? microvmRun);
    assert !(manifest ? virtiofsdRun);
    assert !builtins.any (arg: builtins.match ".*@vsock/.*" arg != null) manifest.ssh.argv;
    true;

  _fixedMachine =
    assert fixedMachineManifest.qemu.memory.sizeMiB == 512;
    assert fixedMachineManifest.qemu.smp.cpus == 2;
    assert vmVirtieFixedMachine.config.microvm.mem == 512;
    assert vmVirtieFixedMachine.config.microvm.vcpu == 2;
    true;

  _balloon =
    assert featureRichManifest.qemu.devices ? balloon;
    assert featureRichManifest.qemu.devices.balloon.id == "balloon0";
    assert !(featureRichManifest.qemu.devices.balloon ? controller);
    assert !(disabledBalloonManifest.qemu.devices ? balloon);
    true;

  _graphical =
    assert graphicalQemuConfig.knobs.noGraphic == false;
    assert graphicalQemuConfig.graphics.backend == "gtk";
    true;

  _externalStoreSocket =
    assert builtins.length externalStoreSocketManifest.qemu.devices.virtiofs == 1;
    assert
      builtins.head externalStoreSocketManifest.qemu.devices.virtiofs == {
        id = "fs0";
        socketPath = "/var/run/virtiofs-nix-store.sock";
        tag = "ro-store";
        transport = "pci";
      };
    assert externalStoreSocketManifest.virtiofs.daemons == [ ];
    true;

  _extraShares =
    assert builtins.length extraSharesManifest.qemu.devices."9p" == 1;
    assert builtins.head extraSharesManifest.qemu.devices."9p" == {
      id = "fs9p0";
      sourcePath = ".agentspace-test/cache";
      tag = "cache";
      securityModel = "mapped";
      readOnly = true;
      transport = "pci";
    };
    assert builtins.any (
      share: share.tag == "tools" && share.socketPath != "" && share.transport == "pci"
    ) extraSharesManifest.qemu.devices.virtiofs;
    assert builtins.any (daemon: daemon.tag == "tools") extraSharesManifest.virtiofs.daemons;
    assert !(builtins.any (daemon: daemon.tag == "cache") extraSharesManifest.virtiofs.daemons);
    true;

  _writeFiles =
    assert featureRichManifest.qemu.guestAgent.socketPath == "qga.sock";
    assert featureRichManifest.writeFiles."/etc/agentspace-inline".chown == "agent:users";
    assert featureRichManifest.writeFiles."/etc/agentspace-inline".text == "hello";
    assert featureRichManifest.writeFiles."/etc/agentspace-inline".mode == "0640";
    assert featureRichManifest.writeFiles."/etc/agentspace-inline".overwrite == true;
    assert featureRichManifest.writeFiles."/etc/agentspace-inline".path == null;
    assert featureRichManifest.writeFiles."/etc/agentspace-host".chown == null;
    assert featureRichManifest.writeFiles."/etc/agentspace-host".text == null;
    assert featureRichManifest.writeFiles."/etc/agentspace-host".mode == null;
    assert featureRichManifest.writeFiles."/etc/agentspace-host".overwrite == false;
    assert featureRichManifest.writeFiles."/etc/agentspace-host".path == ".agentspace-test/host-file";
    true;

  _notifications =
    assert featureRichManifest.notifications.command.path == pkgs.runtimeShell;
    assert
      featureRichManifest.notifications.command.args == [
        "-c"
        notificationCommand
      ];
    assert
      featureRichManifest.notifications.states == [
        "runtime:resume"
        "runtime:suspend"
        "balloon:resize"
      ];
    true;
in
{
  virtie-manifest-contract =
    assert _;
    pkgs.runCommand "virtie-manifest-contract" { } "touch $out";

  virtie-manifest-balloon-contract =
    assert _balloon;
    pkgs.runCommand "virtie-manifest-balloon-contract" { } "touch $out";

  virtie-manifest-graphical-contract =
    assert _graphical;
    pkgs.runCommand "virtie-manifest-graphical-contract" { } "touch $out";

  virtie-manifest-fixed-machine-contract =
    assert _fixedMachine;
    pkgs.runCommand "virtie-manifest-fixed-machine-contract" { } "touch $out";

  virtie-manifest-external-store-socket-contract =
    assert _externalStoreSocket;
    pkgs.runCommand "virtie-manifest-external-store-socket-contract" { } "touch $out";

  virtie-manifest-extra-shares-contract =
    assert _extraShares;
    pkgs.runCommand "virtie-manifest-extra-shares-contract" { } "touch $out";

  virtie-manifest-write-files-contract =
    assert _writeFiles;
    pkgs.runCommand "virtie-manifest-write-files-contract" { } "touch $out";

  virtie-manifest-notifications-contract =
    assert _notifications;
    pkgs.runCommand "virtie-manifest-notifications-contract" { } "touch $out";
}
