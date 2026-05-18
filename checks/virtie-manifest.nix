{
  mkSandbox,
  pkgs,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };
  socatProxyCommand = "ProxyCommand=${pkgs.runtimeShell} -c 'cid=\${1#vsock/}; exec ${pkgs.socat}/bin/socat - VSOCK-CONNECT:$cid:$2' -- %h %p";
  notificationCommand = "notify-send \"virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE\"";

  vmVirtie = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.identityFile = sshKeys.virtie.identityFile;
    persistence.homeImage = null;
  };

  vmDefault = mkSandbox {
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

  vmVirtieEscapedExternalStoreSocket = mkSandbox {
    persistence.homeImage = null;
    mountWorkspace = false;
    nixStoreShareSocket = "/tmp/$(touch injected).sock";
  };

  invalidRelativeStoreSocket = builtins.tryEval (
    (mkSandbox {
      persistence.homeImage = null;
      mountWorkspace = false;
      nixStoreShareSocket = "virtiofs-nix-store.sock";
    }).config.system.build.toplevel.drvPath
  );

  vmVirtieCustomSSHExec = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.virtie.publicKey ];
    ssh.identityFile = sshKeys.virtie.identityFile;
    ssh.exec = [
      "/custom/ssh"
      "-F"
      "custom-config"
    ];
    persistence.homeImage = null;
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
  defaultManifest = vmDefault.config.agentspace.sandbox.launch.virtieManifestData;
  featureRichManifest = vmVirtieFeatureRich.config.agentspace.sandbox.launch.virtieManifestData;
  disabledBalloonManifest =
    vmVirtieBalloonDisabled.config.agentspace.sandbox.launch.virtieManifestData;
  externalStoreSocketManifest =
    vmVirtieExternalStoreSocket.config.agentspace.sandbox.launch.virtieManifestData;
  customSSHExecManifest = vmVirtieCustomSSHExec.config.agentspace.sandbox.launch.virtieManifestData;
  extraSharesManifest = vmVirtieExtraShares.config.agentspace.sandbox.launch.virtieManifestData;
  fixedMachineManifest = vmVirtieFixedMachine.config.agentspace.sandbox.launch.virtieManifestData;
  graphicalManifest = vmVirtieGraphical.config.agentspace.sandbox.launch.virtieManifestData;

  virtiofsMounts = builtins.filter (mount: mount.type == "virtiofs") manifest.mounts;
  virtiofsDaemonMounts = builtins.filter (
    mount: mount.type == "virtiofs" && mount ? virtiofsd_exec
  ) manifest.mounts;

  _ =
    assert builtins.head manifest.qemu.exec != "";
    assert manifest.host_name == "agent-sandbox";
    assert !(manifest ? host);
    assert
      manifest.qemu.fwd_tunnel_exec == [
        "${pkgs.netcat}/bin/nc"
        "$HOST"
        "$PORT"
      ];
    assert manifest.machine.type == "microvm";
    assert !(manifest.qemu ? machine_options);
    assert manifest.state_dir == ".agentspace";
    assert manifest.qemu.qmp_socket == "qmp.sock";
    assert manifest.machine.memory == 4096;
    assert manifest.machine.vcpu == null;
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
    assert builtins.head manifest.ssh.exec == "${pkgs.openssh}/bin/ssh";
    assert builtins.elem socatProxyCommand manifest.ssh.exec;
    assert !(builtins.elem "ProxyUseFdpass=yes" manifest.ssh.exec);
    assert builtins.elem "CheckHostIP=no" manifest.ssh.exec;
    assert manifest.ssh.user == "agent";
    assert manifest.ssh.ready_socket == "ready.sock";
    assert !(manifest.ssh ? retry_delay_ms);
    assert builtins.elem ".agentspace-test/id_ed25519" manifest.ssh.exec;
    assert builtins.any (volume: volume.image == ".agentspace/nix-store-overlay.img") manifest.volumes;
    assert builtins.length virtiofsDaemonMounts > 0;
    assert builtins.all (
      mount: mount.virtiofsd_socket != "" && builtins.head mount.virtiofsd_exec != ""
    ) virtiofsDaemonMounts;
    assert builtins.any (mount: mount.tag == "workspace") virtiofsDaemonMounts;
    assert !(manifest ? vsock);
    assert !(manifest.ssh ? destination);
    assert !(manifest ? qemuConfig);
    assert !(manifest ? microvmRun);
    assert !(manifest ? virtiofsdRun);
    assert !builtins.any (arg: builtins.match ".*@vsock/.*" arg != null) manifest.ssh.exec;
    true;

  _defaultSSH =
    assert defaultManifest.ssh.autoprovision == true;
    assert defaultManifest.ssh.ready_socket == "ready.sock";
    assert vmDefault.config.microvm.virtiofsd.group == null;
    assert !(builtins.elem "-q" defaultManifest.ssh.exec);
    assert builtins.elem socatProxyCommand defaultManifest.ssh.exec;
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
    assert builtins.length externalStoreSocketManifest.mounts == 1;
    assert
      builtins.head externalStoreSocketManifest.mounts == {
        type = "virtiofs";
        source = "/nix/store";
        virtiofsd_socket = "/var/run/virtiofs-nix-store.sock";
        tag = "ro-store";
        read_only = true;
        security_model = "none";
        cache = "auto";
      };
    assert !(builtins.head externalStoreSocketManifest.mounts ? virtiofsd_exec);
    assert pkgs.lib.hasInfix "nixStoreShareSocket does not exist or is not a socket"
      vmVirtieExternalStoreSocket.config.agentspace.sandbox.launch.commonInit;
    assert pkgs.lib.hasInfix
      "nixStoreShareSocket does not exist or is not a socket: '/tmp/$(touch injected).sock'"
      vmVirtieEscapedExternalStoreSocket.config.agentspace.sandbox.launch.commonInit;
    assert
      !(pkgs.lib.hasInfix "nixStoreShareSocket does not exist or is not a socket: /tmp/$(touch injected).sock" vmVirtieEscapedExternalStoreSocket.config.agentspace.sandbox.launch.commonInit);
    assert !invalidRelativeStoreSocket.success;
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
      mount: mount.tag == "tools" && mount.virtiofsd_socket != "" && mount ? virtiofsd_exec
    ) extraSharesManifest.mounts;
    assert
      !(builtins.any (mount: mount.tag == "cache" && mount ? virtiofsd_exec) extraSharesManifest.mounts);
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
      && file.chown == null
      && file.text == null
      && file.mode == null
      && file.overwrite == false
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

  virtie-manifest-notifications-contract =
    assert _notifications;
    pkgs.runCommand "virtie-manifest-notifications-contract" { } "touch $out";
}
