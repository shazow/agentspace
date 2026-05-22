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
          "{{.Config.workspace.hostDir}}/dbus-notifications.sock"
          "--filter"
          "--name={{.Name}}"
          "--workspace={{.Workspace}}"
        ];
        vars.Name = "notifications";
      }
    ];
  };

  manifest = vmVirtie.config.agentspace.sandbox.launch.virtieManifestData;
  defaultManifest = vmDefault.config.agentspace.sandbox.launch.virtieManifestData;
  featureRichManifest = vmVirtieFeatureRich.config.agentspace.sandbox.launch.virtieManifestData;
  disabledBalloonManifest =
    vmVirtieBalloonDisabled.config.agentspace.sandbox.launch.virtieManifestData;
  externalStoreSocketManifest =
    vmVirtieExternalStoreSocket.config.agentspace.sandbox.launch.virtieManifestData;
  customSSHExecManifest = vmVirtieCustomSSHExec.config.agentspace.sandbox.launch.virtieManifestData;
  configFileManifest = vmVirtieConfigFile.config.agentspace.sandbox.launch.virtieManifestData;
  homeSSHPathsManifest = vmVirtieHomeSSHPaths.config.agentspace.sandbox.launch.virtieManifestData;
  extraSharesManifest = vmVirtieExtraShares.config.agentspace.sandbox.launch.virtieManifestData;
  workspaceManifest = vmVirtieWorkspaces.config.agentspace.sandbox.launch.virtieManifestData;
  fixedMachineManifest = vmVirtieFixedMachine.config.agentspace.sandbox.launch.virtieManifestData;
  graphicalManifest = vmVirtieGraphical.config.agentspace.sandbox.launch.virtieManifestData;
  runManifest = vmVirtieRun.config.agentspace.sandbox.launch.virtieManifestData;

  virtiofsMounts = builtins.filter (mount: mount.type == "virtiofs") manifest.mounts;
  virtiofsDaemonMounts = builtins.filter (
    mount: mount.type == "virtiofs" && mount.virtiofs ? bin
  ) manifest.mounts;

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
    assert !(builtins.any (mount: mount.type == "9p") manifest.mounts);
    assert builtins.length manifest.volumes > 0;
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
    assert builtins.any (volume: volume.image == ".agentspace/nix-store-overlay.img") manifest.volumes;
    assert builtins.length virtiofsDaemonMounts > 0;
    assert builtins.all (
      mount: mount.virtiofs.socket != "" && mount.virtiofs.bin != ""
    ) virtiofsDaemonMounts;
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
    assert !(builtins.elem "-q" defaultManifest.ssh.exec);
    assert builtins.elem "ProxyCommand=${pkgs.systemd}/lib/systemd/systemd-ssh-proxy %h %p"
      defaultManifest.ssh.exec;
    assert !(builtins.elem ".agentspace/id_ed25519" defaultManifest.ssh.exec);
    assert defaultManifest.write_files == [ ];
    assert !(manifest.ssh ? autoprovision) || manifest.ssh.autoprovision == false;
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
    assert builtins.length externalStoreSocketManifest.mounts == 3;
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
        security_model = "none";
        cache = "auto";
      }
    ) externalStoreSocketManifest.mounts;
    assert builtins.any (
      mount:
      mount.tag == "workspace"
      && mount.source == ".agentspace/workspace"
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace.sock"
      && mount.virtiofs ? bin
    ) externalStoreSocketManifest.mounts;
    assert builtins.any (
      mount:
      mount.tag == "workspace_cwd"
      && mount.source == "."
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace_cwd.sock"
      && mount.virtiofs ? bin
    ) externalStoreSocketManifest.mounts;
    assert builtins.all (
      mount: mount.tag != "ro-store" || !(mount.virtiofs ? bin)
    ) externalStoreSocketManifest.mounts;
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
    assert
      builtins.length (builtins.filter (mount: mount.type == "9p") extraSharesManifest.mounts) == 1;
    assert builtins.any (
      mount:
      mount.type == "9p"
      && mount.source == ".agentspace-test/cache"
      && mount.tag == "cache"
      && mount.security_model == "mapped"
      && mount.read_only == true
    ) extraSharesManifest.mounts;
    assert builtins.any (
      mount: mount.tag == "tools" && mount.virtiofs.socket != "" && mount.virtiofs ? bin
    ) extraSharesManifest.mounts;
    assert !(builtins.any (mount: mount.tag == "cache" && mount ? virtiofs) extraSharesManifest.mounts);
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
    ) workspaceManifest.mounts;
    assert builtins.any (
      mount:
      mount.tag == "workspace-agentspace"
      && mount.source == "/home/shazow/projects/agentspace"
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace-agentspace.sock"
      && mount.virtiofs ? bin
    ) workspaceManifest.mounts;
    assert builtins.any (
      mount:
      mount.tag == "workspace-project2-foo"
      && mount.source == "/home/shazow/foo"
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace-project2-foo.sock"
      && mount.virtiofs ? bin
    ) workspaceManifest.mounts;
    assert builtins.any (
      mount:
      mount.tag == "workspace_cwd"
      && mount.source == "."
      && mount.virtiofs.socket == "agent-sandbox-virtiofs-workspace_cwd.sock"
      && mount.virtiofs ? bin
    ) workspaceManifest.mounts;
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
            "{{.Config.workspace.hostDir}}/dbus-notifications.sock"
            "--filter"
            "--name={{.Name}}"
            "--workspace={{.Workspace}}"
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
