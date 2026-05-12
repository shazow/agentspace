{
  mkSandbox,
  pkgs,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };
  notificationCommand = "notify-send \"virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE\"";

  vmVirtie = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.identityFile = sshKeys.virtie.identityFile;
    persistence.homeImage = null;
  };

  vmVirtieFeatureRich = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.identityFile = sshKeys.virtie.identityFile;
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
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.identityFile = sshKeys.virtie.identityFile;
    persistence.homeImage = null;
    balloon = false;
  };

  vmVirtieExternalStoreSocket = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.identityFile = sshKeys.virtie.identityFile;
    persistence.homeImage = null;
    mountWorkspace = false;
    nixStoreShareSocket = "/var/run/virtiofs-nix-store.sock";
  };

  vmVirtieExtraShares = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.identityFile = sshKeys.virtie.identityFile;
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
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.identityFile = sshKeys.virtie.identityFile;
    persistence.homeImage = null;
    machine = {
      memory = 512;
      vcpu = 2;
    };
  };

  vmVirtieGraphical = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.identityFile = sshKeys.virtie.identityFile;
    persistence.homeImage = null;
    extraModules = [
      {
        microvm.graphics = {
          enable = true;
          backend = "gtk";
        };
      }
    ];
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
  graphicalManifest = vmVirtieGraphical.config.agentspace.sandbox.launch.virtieManifestData;

  virtiofsMounts = builtins.filter (mount: mount.type == "virtiofs") manifest.mounts;
  virtiofsDaemonMounts = builtins.filter (mount: mount.type == "virtiofs" && mount ? daemon) manifest.mounts;

  _ =
    assert manifest.qemu.binaryPath != "";
    assert manifest.identity.hostName == "agent-sandbox";
    assert manifest.host.system == pkgs.stdenv.hostPlatform.system;
    assert manifest.machine.type == "microvm";
    assert !(manifest.machine ? options);
    assert manifest.paths.runtimeDir == "";
    assert manifest.persistence.baseDir == ".agentspace";
    assert manifest.persistence.stateDir == ".agentspace";
    assert manifest.sockets.qmp == "qmp.sock";
    assert manifest.memory.sizeMiB == 4096;
    assert manifest.machine.vcpu == null;
    assert manifest.graphics == null;
    assert manifest.writeFiles == [ ];
    assert manifest.notifications.states == [ ];
    assert !(manifest.notifications ? command);
    assert builtins.length virtiofsMounts > 0;
    assert !(builtins.any (mount: mount.type == "9p") manifest.mounts);
    assert builtins.length manifest.volumes > 0;
    assert builtins.length manifest.network > 0;
    assert vmVirtie.config.systemd.services.virtie-ssh-signal.after == [ "sshd.service" ];
    assert vmVirtie.config.systemd.services.virtie-ssh-signal.requires == [ "sshd.service" ];
    assert vmVirtie.config.systemd.services.virtie-ssh-signal.serviceConfig.Type == "oneshot";
    assert builtins.head manifest.ssh.argv == "${pkgs.openssh}/bin/ssh";
    assert manifest.ssh.user == "agent";
    assert !(manifest.ssh ? retryDelayMs);
    assert builtins.elem ".agentspace-test/id_ed25519" manifest.ssh.argv;
    assert builtins.any (
      volume: volume.imagePath == ".agentspace/nix-store-overlay.img"
    ) manifest.volumes;
    assert builtins.length virtiofsDaemonMounts > 0;
    assert builtins.all (
      mount: mount.socketPath != "" && mount.daemon.path != ""
    ) virtiofsDaemonMounts;
    assert builtins.any (mount: mount.tag == "workspace") virtiofsDaemonMounts;
    assert !(manifest ? vsock);
    assert !(manifest.ssh ? destination);
    assert !(manifest ? qemuConfig);
    assert !(manifest ? microvmRun);
    assert !(manifest ? virtiofsdRun);
    assert !builtins.any (arg: builtins.match ".*@vsock/.*" arg != null) manifest.ssh.argv;
    true;

  _fixedMachine =
    assert fixedMachineManifest.memory.sizeMiB == 512;
    assert fixedMachineManifest.machine.vcpu == 2;
    assert vmVirtieFixedMachine.config.microvm.mem == 512;
    assert vmVirtieFixedMachine.config.microvm.vcpu == 2;
    true;

  _balloon =
    assert featureRichManifest.balloon.id == "balloon0";
    assert !(featureRichManifest.balloon ? controller);
    assert disabledBalloonManifest.balloon == null;
    true;

  _graphical =
    assert graphicalManifest.graphics.backend == "gtk";
    true;

  _externalStoreSocket =
    assert builtins.length externalStoreSocketManifest.mounts == 1;
    assert
      builtins.head externalStoreSocketManifest.mounts == {
        type = "virtiofs";
        sourcePath = "/nix/store";
        socketPath = "/var/run/virtiofs-nix-store.sock";
        tag = "ro-store";
        readOnly = true;
        securityModel = "none";
        cache = "auto";
      };
    assert !(builtins.head externalStoreSocketManifest.mounts ? daemon);
    true;

  _extraShares =
    assert builtins.length (builtins.filter (mount: mount.type == "9p") extraSharesManifest.mounts) == 1;
    assert builtins.any (
      mount:
      mount.type == "9p"
      && mount.sourcePath == ".agentspace-test/cache"
      && mount.tag == "cache"
      && mount.securityModel == "mapped"
      && mount.readOnly == true
    ) extraSharesManifest.mounts;
    assert builtins.any (
      mount: mount.tag == "tools" && mount.socketPath != "" && mount ? daemon
    ) extraSharesManifest.mounts;
    assert !(builtins.any (mount: mount.tag == "cache" && mount ? daemon) extraSharesManifest.mounts);
    true;

  _writeFiles =
    assert builtins.any (
      file:
      file.guestPath == "/etc/agentspace-inline"
      && file.chown == "agent:users"
      && file.text == "hello"
      && file.mode == "0640"
      && file.overwrite == true
      && file.path == null
    ) featureRichManifest.writeFiles;
    assert builtins.any (
      file:
      file.guestPath == "/etc/agentspace-host"
      && file.chown == null
      && file.text == null
      && file.mode == null
      && file.overwrite == false
      && file.path == ".agentspace-test/host-file"
    ) featureRichManifest.writeFiles;
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
