{
  mkSandbox,
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

  vmVirtieAlpine = mkSandbox {
    alpine = true;
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
    };
    notifications = {
      command = notificationCommand;
      states = [
        "runtime:resume"
        "runtime:suspend"
      ];
    };
  };

  vmVirtieAlpineNoWorkspace = mkSandbox {
    alpine = true;
    mountWorkspace = false;
    persistence.basedir = ".agentspace-alpine";
  };

  manifest = vmVirtie.config.agentspace.sandbox.launch.virtieManifestData;
  featureRichManifest = vmVirtieFeatureRich.config.agentspace.sandbox.launch.virtieManifestData;
  disabledBalloonManifest =
    vmVirtieBalloonDisabled.config.agentspace.sandbox.launch.virtieManifestData;
  externalStoreSocketManifest =
    vmVirtieExternalStoreSocket.config.agentspace.sandbox.launch.virtieManifestData;
  alpineLaunch = vmVirtieAlpine.config.agentspace.sandbox.launch;
  alpineManifest = alpineLaunch.virtieManifestData;
  alpineNoWorkspaceManifest =
    vmVirtieAlpineNoWorkspace.config.agentspace.sandbox.launch.virtieManifestData;

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

  _alpine =
    assert vmVirtieAlpine.config.agentspace.sandbox.alpine;
    assert alpineManifest.qemu.binaryPath == "${pkgs.qemu}/bin/qemu-system-x86_64";
    assert alpineManifest.qemu.name == "agent-sandbox";
    assert alpineManifest.qemu.machine.type == "microvm";
    assert builtins.any (option: option == "pcie=on") alpineManifest.qemu.machine.options;
    assert builtins.match ".*vmlinuz-virt.*" alpineManifest.qemu.kernel.path != null;
    assert builtins.match ".*initramfs-virt.*" alpineManifest.qemu.kernel.initrdPath != null;
    assert builtins.match ".*root=/dev/vda.*" alpineManifest.qemu.kernel.params != null;
    assert alpineManifest.qemu.qmp.socketPath == "qmp.sock";
    assert alpineManifest.qemu.guestAgent.socketPath == "qga.sock";
    assert alpineManifest.qemu.devices.rng.id == "rng0";
    assert alpineManifest.qemu.devices.vsock.id == "vsock0";
    assert alpineManifest.qemu.devices.balloon.id == "balloon0";
    assert builtins.length alpineManifest.qemu.devices.block == 1;
    assert (builtins.head alpineManifest.qemu.devices.block).id == "vda";
    assert (builtins.head alpineManifest.qemu.devices.block).imagePath == ".agentspace/alpine-root.img";
    assert (builtins.head alpineManifest.qemu.devices.block).readOnly == false;
    assert builtins.length alpineManifest.qemu.devices.network == 1;
    assert (builtins.head alpineManifest.qemu.devices.network).backend == "user";
    assert builtins.length alpineManifest.qemu.devices.virtiofs == 1;
    assert (builtins.head alpineManifest.qemu.devices.virtiofs).tag == "workspace";
    assert builtins.length alpineManifest.virtiofs.daemons == 1;
    assert (builtins.head alpineManifest.virtiofs.daemons).tag == "workspace";
    assert !(builtins.any (share: share.tag == "ro-store") alpineManifest.qemu.devices.virtiofs);
    assert alpineNoWorkspaceManifest.qemu.devices.virtiofs == [ ];
    assert alpineNoWorkspaceManifest.virtiofs.daemons == [ ];
    assert alpineManifest.ssh.user == "agent";
    assert builtins.head alpineManifest.ssh.argv == "${pkgs.openssh}/bin/ssh";
    assert builtins.elem ".agentspace-test/id_ed25519" alpineManifest.ssh.argv;
    assert alpineManifest.persistence.baseDir == ".agentspace";
    assert alpineManifest.persistence.stateDir == ".agentspace";
    assert alpineManifest.persistence.directories == [ ".agentspace" ];
    assert alpineManifest.volumes == [ ];
    assert alpineManifest.writeFiles."/etc/agentspace-inline".content == "aGVsbG8=";
    assert alpineManifest.notifications.command.path == pkgs.runtimeShell;
    assert
      alpineManifest.notifications.states == [
        "runtime:resume"
        "runtime:suspend"
      ];
    assert builtins.match ".*Installing Alpine root disk.*" alpineLaunch.commonInit != null;
    assert builtins.match ".*alpine-root.img.*" alpineLaunch.commonInit != null;
    true;
in
{
  virtie-manifest-contract =
    assert _;
    pkgs.runCommand "virtie-manifest-contract" { } "touch $out";

  virtie-manifest-balloon-contract =
    assert _balloon;
    pkgs.runCommand "virtie-manifest-balloon-contract" { } "touch $out";

  virtie-manifest-external-store-socket-contract =
    assert _externalStoreSocket;
    pkgs.runCommand "virtie-manifest-external-store-socket-contract" { } "touch $out";

  virtie-manifest-write-files-contract =
    assert _writeFiles;
    pkgs.runCommand "virtie-manifest-write-files-contract" { } "touch $out";

  virtie-manifest-notifications-contract =
    assert _notifications;
    pkgs.runCommand "virtie-manifest-notifications-contract" { } "touch $out";

  virtie-manifest-alpine-contract =
    assert _alpine;
    pkgs.runCommand "virtie-manifest-alpine-contract" { } "touch $out";
}
