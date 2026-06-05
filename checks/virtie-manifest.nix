{
  mkSandbox,
  mkExecSSH,
  pkgs,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };
  notificationCommand = "notify-send \"virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE\"";

  vmVirtie = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
    };
    persistence.homeImage = null;
    workspace = {
      enable = true;
      addCurrentDir = true;
    };
  };

  vmDefault = mkSandbox {
    persistence.homeImage = null;
  };

  vmQuietDisabled = mkSandbox {
    quiet = false;
    persistence.homeImage = null;
  };

  vmVirtieFeatureRich = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
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

  vmVirtieBalloonDisabled = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
    };
    persistence.homeImage = null;
    balloon = false;
  };

  vmVirtieExternalStoreSocket = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
    };
    persistence.homeImage = null;
    nixStoreShareSocket = "/var/run/virtiofs-nix-store.sock";
  };

  vmVirtieEscapedExternalStoreSocket = mkSandbox {
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

  vmVirtieCustomSSHExec = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = [
      "/custom/ssh"
      "-F"
      "custom-config"
    ];
    persistence.homeImage = null;
  };

  vmVirtieConfigFile = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
      configFile = ".agentspace-test/ssh_config";
    };
    persistence.homeImage = null;
  };

  vmVirtieHomeSSHPaths = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      homeDir = "/home/test";
      identityFile = "~/.ssh/id_ed25519";
      configFile = "~/.ssh/config";
    };
    persistence.homeImage = null;
  };

  vmVirtieExtraShares = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
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

  vmVirtieHotplugMounts = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
    };
    persistence.homeImage = null;
    hotplug.mounts = [
      {
        tag = "cache";
        source = ".agentspace-test/cache";
        target = "/mnt/cache";
        readOnly = true;
      }
      {
        tag = "tools";
        source = ".agentspace-test/tools";
      }
    ];
  };

  vmVirtieMounts = mkSandbox {
    persistence.homeImage = null;
    workspace.enable = false;
    mounts = [
      {
        type = "virtiofs";
        tag = "cache";
        source = ".agentspace-test/cache";
        mountPoint = "/mnt/cache";
        readOnly = true;
      }
      {
        type = "9p";
        tag = "scratch";
        source = ".agentspace-test/scratch";
        mountPoint = "/mnt/scratch";
        securityModel = "mapped";
      }
      {
        type = "image";
        source = ".agentspace-test/data.img";
        mountPoint = "/mnt/data";
        readOnly = true;
        image = {
          size = 512;
          fs = "ext4";
          create = true;
          direct = true;
          serial = "new-data";
        };
      }
    ];
    shares = [
      {
        proto = "virtiofs";
        tag = "legacy-share";
        source = ".agentspace-test/legacy-share";
        mountPoint = "/mnt/legacy-share";
      }
    ];
    volumes = [
      {
        image = ".agentspace-test/legacy.img";
        mountPoint = "/mnt/legacy";
        size = 256;
        fsType = "ext4";
        autoCreate = true;
      }
    ];
  };

  vmVirtieExtraImagesAndPorts = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
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

  vmVirtieWorkspaces = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
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

  vmVirtieSwap = mkSandbox {
    persistence.homeImage = null;
    swapSize = 1024;
    workspace = {
      enable = true;
      guestDir = "/home/agent/workspace";
    };
  };

  vmVirtieFixedMachine = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
    };
    persistence.homeImage = null;
    machine = {
      memory = 512;
      vcpu = 2;
    };
  };

  vmVirtieGraphical = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
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

  vmVirtieRun = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.exec = mkExecSSH {
      identityFile = sshKeys.virtie.identityFile;
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

  vmVirtieInodeFileHandlesPrefer = mkSandbox {
    persistence.homeImage = null;
    extraModules = [
      {
        microvm.virtiofsd.inodeFileHandles = "prefer";
      }
    ];
  };

  vmVirtieNativeInodeFileHandlesPrefer = mkSandbox {
    persistence.homeImage = null;
    virtiofsd.inodeFileHandles = "prefer";
  };

  manifest = vmVirtie.config.agentspace.sandbox.launch.virtieManifestData;
  defaultManifest = vmDefault.config.agentspace.sandbox.launch.virtieManifestData;
  quietDisabledKernelParams = vmQuietDisabled.config.microvm.kernelParams;
  featureRichManifest = vmVirtieFeatureRich.config.agentspace.sandbox.launch.virtieManifestData;
  disabledBalloonManifest =
    vmVirtieBalloonDisabled.config.agentspace.sandbox.launch.virtieManifestData;
  externalStoreSocketManifest =
    vmVirtieExternalStoreSocket.config.agentspace.sandbox.launch.virtieManifestData;
  customSSHExecManifest = vmVirtieCustomSSHExec.config.agentspace.sandbox.launch.virtieManifestData;
  configFileManifest = vmVirtieConfigFile.config.agentspace.sandbox.launch.virtieManifestData;
  homeSSHPathsManifest = vmVirtieHomeSSHPaths.config.agentspace.sandbox.launch.virtieManifestData;
  extraSharesManifest = vmVirtieExtraShares.config.agentspace.sandbox.launch.virtieManifestData;
  hotplugMountsManifest = vmVirtieHotplugMounts.config.agentspace.sandbox.launch.virtieManifestData;
  mountsManifest = vmVirtieMounts.config.agentspace.sandbox.launch.virtieManifestData;
  extraImagesAndPortsManifest =
    vmVirtieExtraImagesAndPorts.config.agentspace.sandbox.launch.virtieManifestData;
  workspaceManifest = vmVirtieWorkspaces.config.agentspace.sandbox.launch.virtieManifestData;
  fixedMachineManifest = vmVirtieFixedMachine.config.agentspace.sandbox.launch.virtieManifestData;
  graphicalManifest = vmVirtieGraphical.config.agentspace.sandbox.launch.virtieManifestData;
  runManifest = vmVirtieRun.config.agentspace.sandbox.launch.virtieManifestData;
  inodeFileHandlesPreferManifest =
    vmVirtieInodeFileHandlesPrefer.config.agentspace.sandbox.launch.virtieManifestData;
  nativeInodeFileHandlesPreferManifest =
    vmVirtieNativeInodeFileHandlesPrefer.config.agentspace.sandbox.launch.virtieManifestData;

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
    assert vmVirtie.config.systemd.services.virtie-ssh-signal.after == [ "sshd.service" ];
    assert vmVirtie.config.systemd.services.virtie-ssh-signal.requires == [ "sshd.service" ];
    assert vmVirtie.config.systemd.services.virtie-ssh-signal.serviceConfig.Type == "oneshot";
    assert
      vmVirtie.config.agentspace.sandbox.ssh.exec == mkExecSSH {
        identityFile = sshKeys.virtie.identityFile;
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
    assert builtins.any (volume: volume.source == ".agentspace/nix-store-overlay.img") (
      mountsOfType "image" manifest
    );
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

  _quietDisabled =
    assert !(builtins.elem "quiet" quietDisabledKernelParams);
    assert !(builtins.elem "udev.log_level=3" quietDisabledKernelParams);
    assert vmQuietDisabled.config.boot.consoleLogLevel != 0;
    assert vmQuietDisabled.config.boot.initrd.verbose != false;
    assert vmQuietDisabled.config.microvm.qemu.serialConsole == false;
    assert vmQuietDisabled.config.agentspace.sandbox.launch.virtieManifestData.kernel.serial == "print";
    assert vmQuietDisabled.config.systemd.services."serial-getty@ttyS0".enable == false;
    assert vmQuietDisabled.config.systemd.services."serial-getty@ttyAMA0".enable == false;
    true;

  _fixedMachine =
    assert fixedMachineManifest.machine.memory == 512;
    assert fixedMachineManifest.machine.vcpu == 2;
    assert vmVirtieFixedMachine.config.microvm.mem == 512;
    assert vmVirtieFixedMachine.config.microvm.vcpu == 2;
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
      vmVirtieExternalStoreSocket.config.agentspace.sandbox.launch.commonInit;
    assert pkgs.lib.hasInfix
      "nixStoreShareSocket does not exist or is not a socket: '/tmp/$(touch injected).sock'"
      vmVirtieEscapedExternalStoreSocket.config.agentspace.sandbox.launch.commonInit;
    assert
      !(pkgs.lib.hasInfix "nixStoreShareSocket does not exist or is not a socket: /tmp/$(touch injected).sock" vmVirtieEscapedExternalStoreSocket.config.agentspace.sandbox.launch.commonInit);
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

  _hotplugMounts =
    assert builtins.length hotplugMountsManifest.hotplug.mounts == 2;
    assert builtins.any (
      mount:
      mount.type == "virtiofs"
      && mount.tag == "cache"
      && mount.source == ".agentspace-test/cache"
      && mount.target == "/mnt/cache"
      && mount.read_only == true
      && mount.virtiofs ? bin
      && mount.virtiofs.args == [ ]
      && !(mount.virtiofs ? socket)
    ) hotplugMountsManifest.hotplug.mounts;
    assert builtins.any (
      mount:
      mount.type == "virtiofs"
      && mount.tag == "tools"
      && mount.source == ".agentspace-test/tools"
      && mount.read_only == false
      && mount.virtiofs ? bin
      && mount.virtiofs.args == [ ]
      && !(mount ? target)
    ) hotplugMountsManifest.hotplug.mounts;
    assert
      !(builtins.any (mount: mount.tag == "cache") (mountsOfType "virtiofs" hotplugMountsManifest));
    assert
      !(builtins.any (mount: mount.tag == "tools") (mountsOfType "virtiofs" hotplugMountsManifest));
    true;

  _mounts =
    assert
      builtins.map (mount: mount.tag) (mountsOfType "virtiofs" mountsManifest) == [
        "ro-store"
        "cache"
        "legacy-share"
      ];
    assert builtins.any (
      mount:
      mount.tag == "cache"
      && mount.source == ".agentspace-test/cache"
      && mount.read_only == true
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-cache.sock"
      && mount.virtiofs ? bin
    ) (mountsOfType "virtiofs" mountsManifest);
    assert builtins.any (
      mount:
      mount.tag == "scratch"
      && mount.source == ".agentspace-test/scratch"
      && mount.read_only == false
      && mount."9p".security_model == "mapped"
    ) (mountsOfType "9p" mountsManifest);
    assert
      builtins.map (mount: mount.source) (mountsOfType "image" mountsManifest) == [
        ".agentspace/nix-store-overlay.img"
        ".agentspace-test/data.img"
        ".agentspace-test/legacy.img"
      ];
    assert builtins.any (
      mount:
      mount.source == ".agentspace-test/data.img"
      && mount.read_only == true
      && mount.image.size == 512
      && mount.image.fs == "ext4"
      && mount.image.create == true
      && mount.image.direct == true
      && mount.image.serial == "new-data"
    ) (mountsOfType "image" mountsManifest);
    assert builtins.any (
      share:
      share.tag == "cache" && share.source == ".agentspace-test/cache" && share.mountPoint == "/mnt/cache"
    ) vmVirtieMounts.config.microvm.shares;
    assert builtins.any (
      volume:
      volume.image == ".agentspace-test/data.img"
      && volume.mountPoint == "/mnt/data"
      && volume.size == 512
    ) vmVirtieMounts.config.microvm.volumes;
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
    assert vmVirtieWorkspaces.config.environment.sessionVariables.WORKSPACE == "/home/agent/workspace";
    assert builtins.elem "d /home/agent/workspace 0755 agent users -"
      vmVirtieWorkspaces.config.systemd.tmpfiles.rules;
    assert builtins.any (
      share:
      share.tag == "workspace"
      && share.source == ".agentspace/workspace"
      && share.mountPoint == "/home/agent/workspace"
    ) vmVirtieWorkspaces.config.microvm.shares;
    assert builtins.any (
      share:
      share.tag == "workspace-agentspace"
      && share.source == "/home/shazow/projects/agentspace"
      && share.mountPoint == "/home/agent/workspace/agentspace"
    ) vmVirtieWorkspaces.config.microvm.shares;
    assert builtins.any (
      share:
      share.tag == "workspace-project2-foo"
      && share.source == "/home/shazow/foo"
      && share.mountPoint == "/home/agent/workspace/project2/foo"
    ) vmVirtieWorkspaces.config.microvm.shares;
    assert builtins.any (
      share: share.tag == "workspace_cwd" && share.source == "." && share.mountPoint == "/mnt/cwd"
    ) vmVirtieWorkspaces.config.microvm.shares;
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
    ) vmVirtieSwap.config.swapDevices;
    assert !(builtins.any (swap: swap.device == "/swapfile") vmVirtieSwap.config.swapDevices);
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
                storeOverlay = "nix-store-overlay.img";
                storeDisk = false;
              };
            };
          };
        }
      ];
    assert !(runManifest ? run_with_tunnel);
    assert !(builtins.any (share: share.tag == "agentspace_tunnels") vmVirtieRun.config.microvm.shares);
    assert
      !(pkgs.lib.hasInfix ".agentspace/tunnels" vmVirtieRun.config.agentspace.sandbox.launch.commonInit);
    true;
in
{
  virtie-manifest-contract =
    assert _;
    pkgs.runCommand "virtie-manifest-contract" { } "touch $out";

  virtie-manifest-default-ssh-contract =
    assert _defaultSSH;
    pkgs.runCommand "virtie-manifest-default-ssh-contract" { } "touch $out";

  virtie-manifest-quiet-disabled-contract =
    assert _quietDisabled;
    pkgs.runCommand "virtie-manifest-quiet-disabled-contract" { } "touch $out";

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

  virtie-manifest-custom-ssh-exec-contract =
    assert _customSSHExec;
    pkgs.runCommand "virtie-manifest-custom-ssh-exec-contract" { } "touch $out";

  virtie-manifest-extra-shares-contract =
    assert _extraShares;
    pkgs.runCommand "virtie-manifest-extra-shares-contract" { } "touch $out";

  virtie-manifest-hotplug-mounts-contract =
    assert _hotplugMounts;
    pkgs.runCommand "virtie-manifest-hotplug-mounts-contract" { } "touch $out";

  virtie-manifest-mounts-contract =
    assert _mounts;
    pkgs.runCommand "virtie-manifest-mounts-contract" { } "touch $out";

  virtie-manifest-extra-images-and-ports-contract =
    assert _extraImagesAndPorts;
    pkgs.runCommand "virtie-manifest-extra-images-and-ports-contract" { } "touch $out";

  virtie-manifest-write-files-contract =
    assert _writeFiles;
    pkgs.runCommand "virtie-manifest-write-files-contract" { } "touch $out";

  virtie-manifest-swap-contract =
    assert _swap;
    pkgs.runCommand "virtie-manifest-swap-contract" { } "touch $out";

  virtie-manifest-notifications-contract =
    assert _notifications;
    pkgs.runCommand "virtie-manifest-notifications-contract" { } "touch $out";

  virtie-manifest-run-contract =
    assert _run;
    pkgs.runCommand "virtie-manifest-run-contract" { } "touch $out";
}
