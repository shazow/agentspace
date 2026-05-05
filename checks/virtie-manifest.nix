{
  mkSandbox,
  mkTinySandbox,
  pkgs,
  ...
}:
let
  testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBIqXkHFLTDd7n09425txXfdOgJDUb7CpMAdCPVRS94z agentspace-virtie-test";
  notificationCommand = "notify-send \"virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE\"";

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
        content = "aGVsbG8=";
        mode = "0640";
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

  vmVirtieFixedMachine = mkSandbox {
    ssh.authorizedKeys = [ testPublicKey ];
    ssh.identityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    machine = {
      memory = 512;
      vcpu = 2;
    };
  };

  vmTinyVirtie = mkTinySandbox {
    hostName = "agent-tiny-test";
    user = "tester";
    machine = {
      memory = 192;
      vcpu = 1;
    };
    ssh.authorizedKeys = [ testPublicKey ];
    ssh.identityFile = ".agentspace-test/id_ed25519";
  };

  vmTinyDefaultMachine = mkTinySandbox {
    hostName = "agent-tiny-default-test";
    ssh.authorizedKeys = [ testPublicKey ];
  };

  manifest = vmVirtie.config.agentspace.sandbox.launch.virtieManifestData;
  featureRichManifest = vmVirtieFeatureRich.config.agentspace.sandbox.launch.virtieManifestData;
  disabledBalloonManifest =
    vmVirtieBalloonDisabled.config.agentspace.sandbox.launch.virtieManifestData;
  externalStoreSocketManifest =
    vmVirtieExternalStoreSocket.config.agentspace.sandbox.launch.virtieManifestData;
  fixedMachineManifest =
    vmVirtieFixedMachine.config.agentspace.sandbox.launch.virtieManifestData;
  tinyManifest = vmTinyVirtie.config.agentspace.tinySandbox.launch.virtieManifestData;
  tinyDefaultManifest =
    vmTinyDefaultMachine.config.agentspace.tinySandbox.launch.virtieManifestData;
  tinyInitrdScript = pkgs.writeText "tiny-initrd-pre-device-commands" vmTinyVirtie.config.boot.initrd.preDeviceCommands;

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
    assert manifest.qemu.memory.sizeMiB == 4096;
    assert !(manifest.qemu.smp ? cpus);
    assert manifest.writeFiles == { };
    assert manifest.notifications.states == [ ];
    assert !(manifest.notifications ? command);
    assert manifest.qemu.devices.rng.id == "rng0";
    assert builtins.length manifest.qemu.devices.virtiofs > 0;
    assert builtins.length manifest.qemu.devices.block > 0;
    assert builtins.length manifest.qemu.devices.network > 0;
    assert manifest.qemu.devices.vsock.id == "vsock0";
    assert builtins.head manifest.ssh.argv == "${pkgs.openssh}/bin/ssh";
    assert manifest.ssh.user == "agent";
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

  _writeFiles =
    assert featureRichManifest.qemu.guestAgent.socketPath == "qga.sock";
    assert featureRichManifest.writeFiles."/etc/agentspace-inline".chown == "agent:users";
    assert featureRichManifest.writeFiles."/etc/agentspace-inline".content == "aGVsbG8=";
    assert featureRichManifest.writeFiles."/etc/agentspace-inline".mode == "0640";
    assert featureRichManifest.writeFiles."/etc/agentspace-inline".path == null;
    assert featureRichManifest.writeFiles."/etc/agentspace-host".chown == null;
    assert featureRichManifest.writeFiles."/etc/agentspace-host".content == null;
    assert featureRichManifest.writeFiles."/etc/agentspace-host".mode == null;
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

  _tiny =
    assert tinyManifest.qemu.binaryPath != "";
    assert tinyManifest.qemu.name == "agent-tiny-test";
    assert tinyManifest.qemu.kernel.initrdPath != "";
    assert tinyManifest.qemu.guestAgent.socketPath == "";
    assert tinyManifest.qemu.memory.sizeMiB == 192;
    assert tinyManifest.qemu.smp.cpus == 1;
    assert tinyManifest.qemu.devices.rng.id == "rng0";
    assert tinyManifest.qemu.devices.vsock.id == "vsock0";
    assert builtins.length tinyManifest.qemu.devices.virtiofs == 0;
    assert builtins.length tinyManifest.qemu.devices.block == 0;
    assert builtins.length tinyManifest.qemu.devices.network > 0;
    assert tinyManifest.volumes == [ ];
    assert tinyManifest.virtiofs.daemons == [ ];
    assert tinyManifest.ssh.user == "tester";
    assert builtins.head tinyManifest.ssh.argv == "${pkgs.openssh}/bin/ssh";
    assert builtins.elem ".agentspace-test/id_ed25519" tinyManifest.ssh.argv;
    assert vmTinyVirtie.config.agentspace.tinySandbox.machine.memory == 192;
    assert vmTinyVirtie.config.agentspace.tinySandbox.machine.vcpu == 1;
    assert vmTinyVirtie.config.microvm.mem == 192;
    assert vmTinyVirtie.config.microvm.vcpu == 1;
    assert vmTinyVirtie.config.microvm.guest.enable == false;
    assert vmTinyVirtie.config.microvm.shares == [ ];
    assert vmTinyVirtie.config.microvm.volumes == [ ];
    assert vmTinyVirtie.config.microvm.storeOnDisk == false;
    assert vmTinyVirtie.config.microvm.writableStoreOverlay == null;
    true;

  _tinyDefaultMachine =
    assert tinyDefaultManifest.qemu.memory.sizeMiB == 256;
    assert !(tinyDefaultManifest.qemu.smp ? cpus);
    assert vmTinyDefaultMachine.config.agentspace.tinySandbox.machine.vcpu == null;
    true;
in
{
  virtie-manifest-contract =
    assert _;
    pkgs.runCommand "virtie-manifest-contract" { } "touch $out";

  virtie-manifest-balloon-contract =
    assert _balloon;
    pkgs.runCommand "virtie-manifest-balloon-contract" { } "touch $out";

  virtie-manifest-fixed-machine-contract =
    assert _fixedMachine;
    pkgs.runCommand "virtie-manifest-fixed-machine-contract" { } "touch $out";

  virtie-manifest-external-store-socket-contract =
    assert _externalStoreSocket;
    pkgs.runCommand "virtie-manifest-external-store-socket-contract" { } "touch $out";

  virtie-manifest-write-files-contract =
    assert _writeFiles;
    pkgs.runCommand "virtie-manifest-write-files-contract" { } "touch $out";

  virtie-manifest-notifications-contract =
    assert _notifications;
    pkgs.runCommand "virtie-manifest-notifications-contract" { } "touch $out";

  virtie-manifest-tiny-contract =
    assert _tiny && _tinyDefaultMachine;
    pkgs.runCommand "virtie-manifest-tiny-contract" { } ''
      test -e ${pkgs.lib.escapeShellArg tinyManifest.qemu.kernel.initrdPath}
      grep -F ${pkgs.lib.escapeShellArg testPublicKey} ${tinyInitrdScript}
      grep -F 'tester:x:1000:100:Agent:/home/tester:/bin/sh' ${tinyInitrdScript}
      grep -F '/home/tester/.ssh/authorized_keys' ${tinyInitrdScript}
      grep -F 'socat1 VSOCK-LISTEN:22' ${tinyInitrdScript}
      grep -F 'sshd -i -e -f /etc/ssh/sshd_config' ${tinyInitrdScript}
      touch $out
    '';
}
