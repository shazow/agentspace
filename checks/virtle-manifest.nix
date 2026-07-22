{
  mkSandbox,
  mkExecSSH,
  pkgs,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };
  notificationCommand = "notify-send \"virtle: $VIRTLE_NOTIFY_STATE - $VIRTLE_NOTIFY_MESSAGE\"";

  vmVirtle = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
    persistence.homeImage = null;
    persistence.storeOverlaySize = 6144;
    workspace = {
      enable = true;
      addCurrentDir = true;
    };
  };

  vmDefault = mkSandbox {
    persistence.homeImage = null;
  };

  vmLocalOverlayStoreDisk = mkSandbox {
    persistence.homeImage = null;
    persistence.storeDisk = true;
  };

  vmNonRootLocalOverlayStore = mkSandbox {
    persistence.homeImage = null;
    extraModules = [ ./local-overlay-store/non-root-daemon.nix ];
  };

  vmQuietDisabled = mkSandbox {
    quiet = false;
    persistence.homeImage = null;
  };

  vmVirtleFeatureRich = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
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
        followLinks = false;
        writeBack = true;
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

  vmVirtleBalloonDisabled = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
    persistence.homeImage = null;
    balloon = false;
  };

  vmVirtleExternalStoreSocket = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
    persistence.homeImage = null;
    nixStoreShareSocket = "/var/run/virtiofs-nix-store.sock";
  };

  vmVirtleEscapedExternalStoreSocket = mkSandbox {
    persistence.homeImage = null;
    nixStoreShareSocket = "/tmp/$(touch injected).sock";
  };

  invalidRelativeStoreSocket = builtins.tryEval (
    (mkSandbox {
      persistence.homeImage = null;
      nixStoreShareSocket = "virtiofs-nix-store.sock";
    }).config.system.build.toplevel.drvPath
  );

  invalidOldPersistenceBaseDir = builtins.tryEval (
    (mkSandbox {
      persistence.basedir = ".agentspace-old";
    }).config.system.build.toplevel.drvPath
  );

  invalidOldWorkspaceBaseDir = builtins.tryEval (
    (mkSandbox {
      workspace.baseDir = "/home/agent/workspace-old";
    }).config.system.build.toplevel.drvPath
  );

  invalidSwapWithoutWorkspace = builtins.tryEval (
    (mkSandbox {
      swapSize = 1024;
      workspace.enable = false;
    }).config.system.build.toplevel.drvPath
  );

  vmVirtleCustomSSHExec = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = [
      "/custom/ssh"
      "-F"
      "custom-config"
    ];
    persistence.homeImage = null;
  };

  vmVirtleConfigFile = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
      configFile = ".agentspace-test/ssh_config";
    };
    persistence.homeImage = null;
  };

  vmVirtleHomeSSHPaths = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      homeDir = "/home/test";
      identityFile = "~/.ssh/id_ed25519";
      configFile = "~/.ssh/config";
    };
    persistence.homeImage = null;
  };

  vmVirtleExtraShares = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
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

  vmVirtleExtraImagesAndPorts = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
    persistence.homeImage = null;
    volumes = [
      {
        image = ".agentspace-test/data.img";
        mountPoint = "/mnt/data";
        size = 512;
        fsType = "ext4";
        autoCreate = true;
        readOnly = true;
        direct = true;
        serial = "cfg-data";
      }
    ];
    forwardPorts = [
      {
        proto = "tcp";
        from = "host";
        host = {
          address = "127.0.0.1";
          port = 2222;
        };
        guest = {
          address = "127.0.0.1";
          port = 22;
        };
      }
    ];
    extraModules = [
      {
        microvm.volumes = [
          {
            image = ".agentspace-test/microvm.img";
            mountPoint = "/mnt/microvm";
            size = 256;
            fsType = "ext4";
            autoCreate = true;
          }
        ];
        microvm.forwardPorts = [
          {
            proto = "udp";
            from = "guest";
            host = {
              address = "127.0.0.1";
              port = 5353;
            };
            guest = {
              address = "127.0.0.1";
              port = 5353;
            };
          }
        ];
      }
    ];
  };

  vmVirtleWorkspaces = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
    persistence.homeImage = null;
    workspace = {
      enable = true;
      guestDir = "/home/agent/workspace";
      addCurrentDir = true;
      spaces = {
        agentspace = "/home/shazow/projects/agentspace";
        "project2/foo" = "/home/shazow/foo";
      };
    };
  };

  vmVirtleSwap = mkSandbox {
    persistence.homeImage = null;
    swapSize = 1024;
    workspace = {
      enable = true;
      guestDir = "/home/agent/workspace";
    };
  };

  vmVirtleFixedMachine = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
    persistence.homeImage = null;
    machine = {
      memory = 512;
      vcpu = 2;
    };
  };

  vmVirtleGraphical = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
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

  vmVirtleRun = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtle.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtle.identityFile;
    };
    persistence.homeImage = null;
    run = [
      {
        exec = [
          "xdg-dbus-proxy"
          "{{.Env.DBUS_SESSION_BUS_ADDRESS}}"
          "{{.Workspace.HostPath}}/dbus-notifications.sock"
          "--filter"
          "--name={{.Name}}"
          "--workspace={{.Workspace.GuestPath}}"
        ];
        vars.Name = "notifications";
      }
    ];
  };

  vmVirtleInodeFileHandlesPrefer = mkSandbox {
    persistence.homeImage = null;
    extraModules = [
      {
        microvm.virtiofsd.inodeFileHandles = "prefer";
      }
    ];
  };

  vmVirtleNativeInodeFileHandlesPrefer = mkSandbox {
    persistence.homeImage = null;
    virtiofsd.inodeFileHandles = "prefer";
  };

  manifest = vmVirtle.config.agentspace.sandbox.launch.virtleManifestData;
  defaultManifest = vmDefault.config.agentspace.sandbox.launch.virtleManifestData;
  quietDisabledKernelParams = vmQuietDisabled.config.microvm.kernelParams;
  featureRichManifest = vmVirtleFeatureRich.config.agentspace.sandbox.launch.virtleManifestData;
  disabledBalloonManifest =
    vmVirtleBalloonDisabled.config.agentspace.sandbox.launch.virtleManifestData;
  externalStoreSocketManifest =
    vmVirtleExternalStoreSocket.config.agentspace.sandbox.launch.virtleManifestData;
  customSSHExecManifest = vmVirtleCustomSSHExec.config.agentspace.sandbox.launch.virtleManifestData;
  configFileManifest = vmVirtleConfigFile.config.agentspace.sandbox.launch.virtleManifestData;
  homeSSHPathsManifest = vmVirtleHomeSSHPaths.config.agentspace.sandbox.launch.virtleManifestData;
  extraSharesManifest = vmVirtleExtraShares.config.agentspace.sandbox.launch.virtleManifestData;
  extraImagesAndPortsManifest =
    vmVirtleExtraImagesAndPorts.config.agentspace.sandbox.launch.virtleManifestData;
  workspaceManifest = vmVirtleWorkspaces.config.agentspace.sandbox.launch.virtleManifestData;
  fixedMachineManifest = vmVirtleFixedMachine.config.agentspace.sandbox.launch.virtleManifestData;
  graphicalManifest = vmVirtleGraphical.config.agentspace.sandbox.launch.virtleManifestData;
  runManifest = vmVirtleRun.config.agentspace.sandbox.launch.virtleManifestData;
  inodeFileHandlesPreferManifest =
    vmVirtleInodeFileHandlesPrefer.config.agentspace.sandbox.launch.virtleManifestData;
  nativeInodeFileHandlesPreferManifest =
    vmVirtleNativeInodeFileHandlesPrefer.config.agentspace.sandbox.launch.virtleManifestData;

  mountsOfType = type: manifest: builtins.filter (mount: mount.type == type) manifest.mounts;
  virtiofsMounts = mountsOfType "virtiofs" manifest;
  ninePMounts = mountsOfType "9p" manifest;
  virtiofsDaemonMounts = builtins.filter (mount: mount.virtiofs ? bin) virtiofsMounts;
  virtiofsDaemonScript = builtins.readFile (builtins.head virtiofsDaemonMounts).virtiofs.bin;
  inodeFileHandlesPreferVirtiofsDaemonMounts = builtins.filter (mount: mount.virtiofs ? bin) (
    mountsOfType "virtiofs" inodeFileHandlesPreferManifest
  );
  inodeFileHandlesPreferVirtiofsDaemonScript = builtins.readFile (builtins.head inodeFileHandlesPreferVirtiofsDaemonMounts)
  .virtiofs.bin;
  nativeInodeFileHandlesPreferVirtiofsDaemonMounts = builtins.filter (mount: mount.virtiofs ? bin) (
    mountsOfType "virtiofs" nativeInodeFileHandlesPreferManifest
  );
  nativeInodeFileHandlesPreferVirtiofsDaemonScript = builtins.readFile (builtins.head nativeInodeFileHandlesPreferVirtiofsDaemonMounts)
  .virtiofs.bin;

  _ =
    assert builtins.head manifest.qemu.exec != "";
    assert manifest.host_name == "agent-sandbox";
    assert !(manifest ? host);
    assert
      manifest.qemu.fwd_tunnel_exec == [
        "${pkgs.netcat}/bin/nc"
        "{{.Host}}"
        "{{.Port}}"
      ];
    assert manifest.machine.type == "microvm";
    assert !(manifest.qemu ? machine_options);
    assert manifest.state_dir == ".agentspace";
    assert manifest.qemu.qmp_socket == "qmp.sock";
    assert manifest.machine.memory == 4096;
    assert !(manifest.machine ? vcpu);
    assert manifest.graphics.backend == "headless";
    assert manifest.write_files == [ ];
    assert manifest.notifications.states == [ ];
    assert !(manifest.notifications ? exec);
    assert builtins.length virtiofsMounts > 0;
    assert ninePMounts == [ ];
    assert !(manifest ? volumes);
    assert builtins.length (mountsOfType "image" manifest) > 0;
    assert builtins.length manifest.networks > 0;
    assert vmVirtle.config.systemd.services.virtle-ssh-signal.after == [ "sshd.service" ];
    assert vmVirtle.config.systemd.services.virtle-ssh-signal.requires == [ "sshd.service" ];
    assert vmVirtle.config.systemd.services.virtle-ssh-signal.serviceConfig.Type == "oneshot";
    assert
      vmVirtle.config.agentspace.sandbox.ssh.exec == mkExecSSH {
        identityFile = sshKeys.virtle.identityFile;
      };
    assert builtins.head manifest.ssh.exec == "${pkgs.openssh}/bin/ssh";
    assert builtins.elem "ProxyCommand=${pkgs.systemd}/lib/systemd/systemd-ssh-proxy %h %p"
      manifest.ssh.exec;
    assert builtins.elem "ProxyUseFdpass=yes" manifest.ssh.exec;
    assert builtins.elem "CheckHostIP=no" manifest.ssh.exec;
    assert manifest.ssh.user == "agent";
    assert manifest.ssh.ready_socket == "ready.sock";
    assert !(manifest.ssh ? retry_delay_ms);
    assert builtins.elem ".agentspace-test/id_ed25519" manifest.ssh.exec;
    assert builtins.any (
      volume: volume.source == ".agentspace/nix-store-overlay-v2.img" && volume.image.size == 6144
    ) (mountsOfType "image" manifest);
    assert builtins.length virtiofsDaemonMounts > 0;
    assert builtins.all (
      mount: mount.virtiofs.socket != "" && mount.virtiofs.bin != ""
    ) virtiofsDaemonMounts;
    assert !(pkgs.lib.hasInfix "--sandbox=none" virtiofsDaemonScript);
    assert !(pkgs.lib.hasInfix "--sandbox=namespace" virtiofsDaemonScript);
    assert pkgs.lib.hasInfix "ulimit -Hn" virtiofsDaemonScript;
    assert pkgs.lib.hasInfix "--inode-file-handles=never" virtiofsDaemonScript;
    assert pkgs.lib.hasInfix "--inode-file-handles=prefer" inodeFileHandlesPreferVirtiofsDaemonScript;
    assert pkgs.lib.hasInfix "--inode-file-handles=prefer"
      nativeInodeFileHandlesPreferVirtiofsDaemonScript;
    assert builtins.any (mount: mount.tag == "workspace_cwd") virtiofsDaemonMounts;
    assert !(manifest ? vsock);
    assert !(manifest.ssh ? destination);
    assert !(manifest ? qemuConfig);
    assert !(manifest ? microvmRun);
    assert !(manifest ? virtiofsdRun);
    assert !builtins.any (arg: builtins.match ".*@vsock/.*" arg != null) manifest.ssh.exec;
    _configFile;

  _defaultSSH =
    assert defaultManifest.ssh.autoprovision == true;
    assert defaultManifest.ssh.ready_socket == "ready.sock";
    assert vmDefault.config.microvm.virtiofsd.group == null;
    assert vmDefault.config.microvm.virtiofsd.inodeFileHandles == "never";
    assert vmDefault.config.agentspace.sandbox.virtiofsd.inodeFileHandles == "never";
    assert vmDefault.config.microvm.qemu.serialConsole == false;
    assert defaultManifest.kernel.serial == "off";
    assert builtins.elem "quiet" vmDefault.config.microvm.kernelParams;
    assert builtins.elem "udev.log_level=3" vmDefault.config.microvm.kernelParams;
    assert !(builtins.elem "-q" defaultManifest.ssh.exec);
    assert builtins.elem "ProxyCommand=${pkgs.systemd}/lib/systemd/systemd-ssh-proxy %h %p"
      defaultManifest.ssh.exec;
    assert !(builtins.elem ".agentspace/id_ed25519" defaultManifest.ssh.exec);
    assert defaultManifest.write_files == [ ];
    assert !(manifest.ssh ? autoprovision) || manifest.ssh.autoprovision == false;
    true;

  _localOverlayStore =
    let
      defaultConfig = vmDefault.config;
      storeMount = defaultConfig.fileSystems."/nix/store";
      lowerStoreViewMount = defaultConfig.fileSystems."/nix/.local-overlay-lower-store";
      daemonService = defaultConfig.systemd.services.nix-daemon;
      daemonEnvironment = builtins.readFile daemonService.serviceConfig.EnvironmentFile;
      nonRootTmpfiles = vmNonRootLocalOverlayStore.config.systemd.tmpfiles.rules;
    in
    assert defaultConfig.microvm.writableStoreOverlay == null;
    assert storeMount.neededForBoot;
    assert storeMount.overlay.lowerdir == [ "/nix/.ro-store" ];
    assert storeMount.overlay.upperdir == "/nix/.rw-store/store";
    assert storeMount.overlay.workdir == "/nix/.rw-store/work";
    assert lowerStoreViewMount.overlay.lowerdir == [ "/nix/.ro-store" ];
    assert lowerStoreViewMount.overlay.upperdir == "/nix/.rw-store/lower-store-view";
    assert lowerStoreViewMount.overlay.workdir == "/nix/.rw-store/lower-store-view-work";
    assert defaultConfig.fileSystems."/nix/.rw-store".neededForBoot;
    assert builtins.elem "overlay" defaultConfig.boot.initrd.kernelModules;
    assert builtins.elem "local-overlay-store" defaultConfig.nix.settings.experimental-features;
    assert builtins.elem "read-only-local-store" defaultConfig.nix.settings.experimental-features;
    assert pkgs.lib.hasPrefix "NIX_REMOTE=local-overlay://" daemonEnvironment;
    assert pkgs.lib.hasInfix "read-only%3Dtrue" daemonEnvironment;
    assert pkgs.lib.hasInfix "check-mount=false" daemonEnvironment;
    assert builtins.elem "post-boot.service" daemonService.after;
    assert builtins.elem "/nix/store" daemonService.unitConfig.RequiresMountsFor;
    assert builtins.elem "/nix/.local-overlay-lower-store" daemonService.unitConfig.RequiresMountsFor;
    assert builtins.elem "/nix/.rw-store/state" daemonService.unitConfig.RequiresMountsFor;
    assert builtins.elem "d /nix/.rw-store/state 0755 nix-daemon nix-daemon - -" nonRootTmpfiles;
    assert builtins.elem "d /nix/.local-overlay-lower-store 0755 nix-daemon nix-daemon - -"
      nonRootTmpfiles;
    assert builtins.elem "d /nix/.local-overlay-lower-store/.links 0755 nix-daemon nix-daemon - -"
      nonRootTmpfiles;
    assert vmLocalOverlayStoreDisk.config.fileSystems."/nix/.ro-store".neededForBoot;
    assert vmLocalOverlayStoreDisk.config.fileSystems."/nix/.ro-store".fsType == "erofs";
    assert
      vmLocalOverlayStoreDisk.config.fileSystems."/nix/.ro-store".device
      == "/dev/disk/by-label/nix-store";
    true;

  _quietDisabled =
    assert !(builtins.elem "quiet" quietDisabledKernelParams);
    assert !(builtins.elem "udev.log_level=3" quietDisabledKernelParams);
    assert vmQuietDisabled.config.boot.consoleLogLevel != 0;
    assert vmQuietDisabled.config.boot.initrd.verbose != false;
    assert vmQuietDisabled.config.microvm.qemu.serialConsole == false;
    assert vmQuietDisabled.config.agentspace.sandbox.launch.virtleManifestData.kernel.serial == "print";
    assert vmQuietDisabled.config.systemd.services."serial-getty@ttyS0".enable == false;
    assert vmQuietDisabled.config.systemd.services."serial-getty@ttyAMA0".enable == false;
    true;

  _fixedMachine =
    assert fixedMachineManifest.machine.memory == 512;
    assert fixedMachineManifest.machine.vcpu == 2;
    assert vmVirtleFixedMachine.config.microvm.mem == 512;
    assert vmVirtleFixedMachine.config.microvm.vcpu == 2;
    true;

  _balloon =
    assert featureRichManifest.balloon.enabled == true;
    assert !(featureRichManifest.balloon ? controller);
    assert disabledBalloonManifest.balloon.enabled == false;
    true;

  _graphical =
    assert graphicalManifest.graphics.backend == "gtk";
    true;

  _externalStoreSocket =
    assert builtins.length (mountsOfType "virtiofs" externalStoreSocketManifest) == 3;
    assert (mountsOfType "9p" externalStoreSocketManifest) == [ ];
    assert builtins.any (
      mount:
      mount == {
        type = "virtiofs";
        source = "/nix/store";
        virtiofs = {
          socket = "/var/run/virtiofs-nix-store.sock";
        };
        tag = "ro-store";
        read_only = true;
      }
    ) (mountsOfType "virtiofs" externalStoreSocketManifest);
    assert builtins.any (
      mount:
      mount.tag == "workspace"
      && mount.source == ".agentspace/workspace"
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace.sock"
      && mount.virtiofs ? bin
    ) (mountsOfType "virtiofs" externalStoreSocketManifest);
    assert builtins.any (
      mount:
      mount.tag == "workspace_cwd"
      && mount.source == "."
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace_cwd.sock"
      && mount.virtiofs ? bin
    ) (mountsOfType "virtiofs" externalStoreSocketManifest);
    assert builtins.all (mount: mount.tag != "ro-store" || !(mount.virtiofs ? bin)) (
      mountsOfType "virtiofs" externalStoreSocketManifest
    );
    assert pkgs.lib.hasInfix "nixStoreShareSocket does not exist or is not a socket"
      vmVirtleExternalStoreSocket.config.agentspace.sandbox.launch.commonInit;
    assert pkgs.lib.hasInfix
      "nixStoreShareSocket does not exist or is not a socket: '/tmp/$(touch injected).sock'"
      vmVirtleEscapedExternalStoreSocket.config.agentspace.sandbox.launch.commonInit;
    assert
      !(pkgs.lib.hasInfix "nixStoreShareSocket does not exist or is not a socket: /tmp/$(touch injected).sock" vmVirtleEscapedExternalStoreSocket.config.agentspace.sandbox.launch.commonInit);
    assert !invalidRelativeStoreSocket.success;
    assert !invalidOldPersistenceBaseDir.success;
    assert !invalidOldWorkspaceBaseDir.success;
    true;

  _customSSHExec =
    assert
      customSSHExecManifest.ssh.exec == [
        "/custom/ssh"
        "-F"
        "custom-config"
      ];
    assert !(builtins.elem "ProxyUseFdpass=yes" customSSHExecManifest.ssh.exec);
    assert !(builtins.elem ".agentspace-test/id_ed25519" customSSHExecManifest.ssh.exec);
    true;

  _configFile =
    assert builtins.elem "-F" configFileManifest.ssh.exec;
    assert builtins.elem ".agentspace-test/ssh_config" configFileManifest.ssh.exec;
    assert builtins.elem ".agentspace-test/id_ed25519" configFileManifest.ssh.exec;
    true;

  _homeSSHPaths =
    assert builtins.elem "-F" homeSSHPathsManifest.ssh.exec;
    assert builtins.elem "/home/test/.ssh/config" homeSSHPathsManifest.ssh.exec;
    assert builtins.elem "/home/test/.ssh/id_ed25519" homeSSHPathsManifest.ssh.exec;
    true;

  _extraShares =
    assert builtins.length (mountsOfType "9p" extraSharesManifest) == 1;
    assert builtins.any (
      mount:
      mount.source == ".agentspace-test/cache"
      && mount.tag == "cache"
      && mount."9p".security_model == "mapped"
      && mount.read_only == true
    ) (mountsOfType "9p" extraSharesManifest);
    assert builtins.any (
      mount: mount.tag == "tools" && mount.virtiofs.socket != "" && mount.virtiofs ? bin
    ) (mountsOfType "virtiofs" extraSharesManifest);
    assert !(builtins.any (mount: mount.tag == "cache") (mountsOfType "virtiofs" extraSharesManifest));
    true;

  _extraImagesAndPorts =
    assert !(extraImagesAndPortsManifest ? volumes);
    assert builtins.any (
      mount:
      mount.source == ".agentspace-test/data.img"
      && mount.read_only == true
      && mount.image.size == 512
      && mount.image.fs == "ext4"
      && mount.image.create == true
      && mount.image.direct == true
      && mount.image.serial == "cfg-data"
    ) (mountsOfType "image" extraImagesAndPortsManifest);
    assert builtins.any (
      mount:
      mount.source == ".agentspace-test/microvm.img"
      && mount.image.size == 256
      && mount.image.create == true
    ) (mountsOfType "image" extraImagesAndPortsManifest);
    assert builtins.any (
      network:
      builtins.any (
        forward:
        forward.proto == "tcp"
        && forward.from == "host"
        && forward.host == "127.0.0.1:2222"
        && forward.guest == "127.0.0.1:22"
      ) network.forward
    ) extraImagesAndPortsManifest.networks;
    assert builtins.any (
      network:
      builtins.any (
        forward:
        forward.proto == "udp"
        && forward.from == "guest"
        && forward.host == "127.0.0.1:5353"
        && forward.guest == "127.0.0.1:5353"
      ) network.forward
    ) extraImagesAndPortsManifest.networks;
    true;

  _workspace =
    assert workspaceManifest.workspace.guest_dir == "/home/agent/workspace";
    assert workspaceManifest.workspace.host_dir == ".agentspace/workspace";
    assert workspaceManifest.workspace.mount_cwd == true;
    assert vmVirtleWorkspaces.config.environment.sessionVariables.WORKSPACE == "/home/agent/workspace";
    assert builtins.elem "d /home/agent/workspace 0755 agent users -"
      vmVirtleWorkspaces.config.systemd.tmpfiles.rules;
    assert builtins.any (
      share:
      share.tag == "workspace"
      && share.source == ".agentspace/workspace"
      && share.mountPoint == "/home/agent/workspace"
    ) vmVirtleWorkspaces.config.microvm.shares;
    assert builtins.any (
      share:
      share.tag == "workspace-agentspace"
      && share.source == "/home/shazow/projects/agentspace"
      && share.mountPoint == "/home/agent/workspace/agentspace"
    ) vmVirtleWorkspaces.config.microvm.shares;
    assert builtins.any (
      share:
      share.tag == "workspace-project2-foo"
      && share.source == "/home/shazow/foo"
      && share.mountPoint == "/home/agent/workspace/project2/foo"
    ) vmVirtleWorkspaces.config.microvm.shares;
    assert builtins.any (
      share: share.tag == "workspace_cwd" && share.source == "." && share.mountPoint == "/mnt/cwd"
    ) vmVirtleWorkspaces.config.microvm.shares;
    assert builtins.any (
      mount:
      mount.tag == "workspace"
      && mount.source == ".agentspace/workspace"
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace.sock"
      && mount.virtiofs ? bin
    ) (mountsOfType "virtiofs" workspaceManifest);
    assert builtins.any (
      mount:
      mount.tag == "workspace-agentspace"
      && mount.source == "/home/shazow/projects/agentspace"
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace-agentspace.sock"
      && mount.virtiofs ? bin
    ) (mountsOfType "virtiofs" workspaceManifest);
    assert builtins.any (
      mount:
      mount.tag == "workspace-project2-foo"
      && mount.source == "/home/shazow/foo"
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace-project2-foo.sock"
      && mount.virtiofs ? bin
    ) (mountsOfType "virtiofs" workspaceManifest);
    assert builtins.any (
      mount:
      mount.tag == "workspace_cwd"
      && mount.source == "."
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace_cwd.sock"
      && mount.virtiofs ? bin
    ) (mountsOfType "virtiofs" workspaceManifest);
    true;

  _swap =
    assert builtins.any (
      swap: swap.device == "/home/agent/workspace/swapfile" && swap.size == 1024
    ) vmVirtleSwap.config.swapDevices;
    assert !(builtins.any (swap: swap.device == "/swapfile") vmVirtleSwap.config.swapDevices);
    assert !invalidSwapWithoutWorkspace.success;
    true;

  _writeFiles =
    assert builtins.any (
      file:
      file.guest_path == "/etc/agentspace-inline"
      && file.chown == "agent:users"
      && file.text == "hello"
      && file.mode == "0640"
      && file.overwrite == true
      && !(file ? source)
    ) featureRichManifest.write_files;
    assert builtins.any (
      file:
      file.guest_path == "/etc/agentspace-host"
      && !(file ? chown)
      && !(file ? text)
      && !(file ? mode)
      && (file.overwrite or false) == false
      && file.follow_links == false
      && file.write_back == true
      && file.source == ".agentspace-test/host-file"
    ) featureRichManifest.write_files;
    true;

  _notifications =
    assert
      featureRichManifest.notifications.exec == [
        pkgs.runtimeShell
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

  _run =
    assert
      runManifest.run == [
        {
          exec = [
            "xdg-dbus-proxy"
            "{{.Env.DBUS_SESSION_BUS_ADDRESS}}"
            "{{.Workspace.HostPath}}/dbus-notifications.sock"
            "--filter"
            "--name={{.Name}}"
            "--workspace={{.Workspace.GuestPath}}"
          ];
          vars = {
            Name = "notifications";
            Config = {
              hostName = "agent-sandbox";
              user = "agent";
              workspace = {
                enable = true;
                guestDir = "/home/agent/workspace";
                hostDir = ".agentspace/workspace";
                addCurrentDir = true;
                spaces = { };
              };
              persistence = {
                baseDir = ".agentspace";
                homeImage = null;
                homeSize = 4096;
                storeOverlay = "nix-store-overlay-v2.img";
                storeOverlaySize = 8192;
                storeDisk = false;
              };
            };
          };
        }
      ];
    assert !(runManifest ? run_with_tunnel);
    assert !(builtins.any (share: share.tag == "agentspace_tunnels") vmVirtleRun.config.microvm.shares);
    assert
      !(pkgs.lib.hasInfix ".agentspace/tunnels" vmVirtleRun.config.agentspace.sandbox.launch.commonInit);
    true;
in
{
  virtle-manifest-contract =
    assert _;
    pkgs.runCommand "virtle-manifest-contract" { } "touch $out";

  virtle-manifest-default-ssh-contract =
    assert _defaultSSH;
    pkgs.runCommand "virtle-manifest-default-ssh-contract" { } "touch $out";

  virtle-manifest-local-overlay-store-contract =
    assert _localOverlayStore;
    pkgs.runCommand "virtle-manifest-local-overlay-store-contract" { } "touch $out";

  virtle-manifest-quiet-disabled-contract =
    assert _quietDisabled;
    pkgs.runCommand "virtle-manifest-quiet-disabled-contract" { } "touch $out";

  virtle-manifest-balloon-contract =
    assert _balloon;
    pkgs.runCommand "virtle-manifest-balloon-contract" { } "touch $out";

  virtle-manifest-graphical-contract =
    assert _graphical;
    pkgs.runCommand "virtle-manifest-graphical-contract" { } "touch $out";

  virtle-manifest-fixed-machine-contract =
    assert _fixedMachine;
    pkgs.runCommand "virtle-manifest-fixed-machine-contract" { } "touch $out";

  virtle-manifest-external-store-socket-contract =
    assert _externalStoreSocket;
    pkgs.runCommand "virtle-manifest-external-store-socket-contract" { } "touch $out";

  virtle-manifest-custom-ssh-exec-contract =
    assert _customSSHExec;
    pkgs.runCommand "virtle-manifest-custom-ssh-exec-contract" { } "touch $out";

  virtle-manifest-extra-shares-contract =
    assert _extraShares;
    pkgs.runCommand "virtle-manifest-extra-shares-contract" { } "touch $out";

  virtle-manifest-extra-images-and-ports-contract =
    assert _extraImagesAndPorts;
    pkgs.runCommand "virtle-manifest-extra-images-and-ports-contract" { } "touch $out";

  virtle-manifest-write-files-contract =
    assert _writeFiles;
    pkgs.runCommand "virtle-manifest-write-files-contract" { } "touch $out";

  virtle-manifest-swap-contract =
    assert _swap;
    pkgs.runCommand "virtle-manifest-swap-contract" { } "touch $out";

  virtle-manifest-notifications-contract =
    assert _notifications;
    pkgs.runCommand "virtle-manifest-notifications-contract" { } "touch $out";

  virtle-manifest-run-contract =
    assert _run;
    pkgs.runCommand "virtle-manifest-run-contract" { } "touch $out";
}
