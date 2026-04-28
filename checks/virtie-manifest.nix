{
  mkSandbox,
  pkgs,
  ...
}:
let
  testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBIqXkHFLTDd7n09425txXfdOgJDUb7CpMAdCPVRS94z agentspace-virtie-test";

  vmVirtie = mkSandbox {
    sshAuthorizedKeys = [ testPublicKey ];
    sshIdentityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
  };

  vmVirtieBalloonEnabled = mkSandbox {
    sshAuthorizedKeys = [ testPublicKey ];
    sshIdentityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    balloon = true;
  };

  vmVirtieBalloonDisabled = mkSandbox {
    sshAuthorizedKeys = [ testPublicKey ];
    sshIdentityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    balloon = false;
  };

  vmVirtieExternalStoreSocket = mkSandbox {
    sshAuthorizedKeys = [ testPublicKey ];
    sshIdentityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    mountWorkspace = false;
    nixStoreShareSocket = "/var/run/virtiofs-nix-store.sock";
  };

  vmVirtieWriteFiles = mkSandbox {
    sshAuthorizedKeys = [ testPublicKey ];
    sshIdentityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
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
  };

  vmVirtieNotifications = mkSandbox {
    sshAuthorizedKeys = [ testPublicKey ];
    sshIdentityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    notifications = {
      command = {
        path = "/run/current-system/sw/bin/sh";
        args = [
          "-c"
          "notify-send \"virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE\""
        ];
      };
      states = [
        "runtime:resume"
        "runtime:suspend"
        "balloon:resize"
      ];
    };
  };

  manifest = vmVirtie.config.agentspace.sandbox.launch.virtieManifestData;
  enabledBalloonManifest = vmVirtieBalloonEnabled.config.agentspace.sandbox.launch.virtieManifestData;
  disabledBalloonManifest =
    vmVirtieBalloonDisabled.config.agentspace.sandbox.launch.virtieManifestData;
  externalStoreSocketManifest =
    vmVirtieExternalStoreSocket.config.agentspace.sandbox.launch.virtieManifestData;
  writeFilesManifest = vmVirtieWriteFiles.config.agentspace.sandbox.launch.virtieManifestData;
  notificationsManifest =
    vmVirtieNotifications.config.agentspace.sandbox.launch.virtieManifestData;

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
    assert builtins.any (volume: volume.imagePath == ".agentspace/nix-store-overlay.img") manifest.volumes;
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
    assert enabledBalloonManifest.qemu.devices ? balloon;
    assert enabledBalloonManifest.qemu.devices.balloon.id == "balloon0";
    assert !(enabledBalloonManifest.qemu.devices.balloon ? controller);
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
    assert writeFilesManifest.qemu.guestAgent.socketPath == "qga.sock";
    assert writeFilesManifest.writeFiles."/etc/agentspace-inline".chown == "agent:users";
    assert writeFilesManifest.writeFiles."/etc/agentspace-inline".content == "aGVsbG8=";
    assert writeFilesManifest.writeFiles."/etc/agentspace-inline".mode == "0640";
    assert writeFilesManifest.writeFiles."/etc/agentspace-inline".path == null;
    assert writeFilesManifest.writeFiles."/etc/agentspace-host".chown == null;
    assert writeFilesManifest.writeFiles."/etc/agentspace-host".content == null;
    assert writeFilesManifest.writeFiles."/etc/agentspace-host".mode == null;
    assert writeFilesManifest.writeFiles."/etc/agentspace-host".path == ".agentspace-test/host-file";
    true;

  _notifications =
    assert notificationsManifest.notifications.command.path == "/run/current-system/sw/bin/sh";
    assert notificationsManifest.notifications.command.args == [
      "-c"
      "notify-send \"virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE\""
    ];
    assert notificationsManifest.notifications.states == [
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

  virtie-manifest-external-store-socket-contract =
    assert _externalStoreSocket;
    pkgs.runCommand "virtie-manifest-external-store-socket-contract" { } "touch $out";

  virtie-manifest-write-files-contract =
    assert _writeFiles;
    pkgs.runCommand "virtie-manifest-write-files-contract" { } "touch $out";

  virtie-manifest-notifications-contract =
    assert _notifications;
    pkgs.runCommand "virtie-manifest-notifications-contract" { } "touch $out";
}
