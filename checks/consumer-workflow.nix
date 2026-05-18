{
  mkLaunch,
  mkSandbox,
  pkgs,
  ...
}:
let
  sshKeys = import ./ssh-keys.nix { inherit pkgs; };

  vmConsumer = mkSandbox {
    ssh.authorizedKeys = [ sshKeys.consumer.publicKey ];
    ssh.identityFile = sshKeys.consumer.identityFile;
    ssh.command = "bash -lc pwd";
    groups = [
      "wheel"
      "kvm"
    ];
    persistence = {
      homeImage = "/var/lib/agentspace/home.img";
      storeOverlay = "/var/lib/agentspace/nix-store-overlay.img";
    };
    machine = {
      memory = 512;
      vcpu = 16;
    };
    extraModules = [
      {
        agentspace.sandbox.extraModules = [
          {
            environment.variables.AGENTSPACE_EXTRA_MODULE = "1";
          }
        ];
      }
    ];
    homeModules = [
      (
        { pkgs, ... }:
        {
          home.packages = [
            pkgs.hello
          ];
          home.sessionVariables.AGENTSPACE_HM = "1";
          programs.git.enable = true;
        }
      )
    ];
  };

  unsupportedFixedCID = builtins.tryEval (
    (mkSandbox {
      extraModules = [
        {
          agentspace.sandbox.extraModules = [
            {
              microvm.vsock.cid = 42;
            }
          ];
        }
      ];
    }).config.system.build.toplevel.drvPath
  );

  sandboxCfg = vmConsumer.config.agentspace.sandbox;
  userCfg = vmConsumer.config.users.users.${sandboxCfg.user};
  homeCfg = vmConsumer.config.home-manager.users.${sandboxCfg.user};
  manifestPath = sandboxCfg.launch.virtieManifest;
  manifestTemplate = sandboxCfg.launch.virtieManifestTemplate;
  manifest = sandboxCfg.launch.virtieManifestData;
  launchScript = mkLaunch vmConsumer;
  runner = vmConsumer.config.microvm.declaredRunner.outPath;
  virtiofsdHelper = "${runner}/bin/virtiofsd-run";

  consumerWorkflow =
    assert sandboxCfg.ssh.identityFile == sshKeys.consumer.identityFile;
    assert sandboxCfg.ssh.command == "bash -lc pwd";
    assert sandboxCfg.ssh.autoconnect == true;
    assert sandboxCfg.persistence.homeImage == "/var/lib/agentspace/home.img";
    assert sandboxCfg.persistence.storeOverlay == "/var/lib/agentspace/nix-store-overlay.img";
    assert sandboxCfg.machine.memory == 512;
    assert sandboxCfg.machine.vcpu == 16;
    assert sandboxCfg.groups == [
      "wheel"
      "kvm"
    ];
    assert builtins.length sandboxCfg.extraModules == 1;
    assert vmConsumer.config.microvm.vcpu == 16;
    assert vmConsumer.config.microvm.mem == 512;
    assert vmConsumer.config.microvm.vsock.cid == null;
    assert vmConsumer.config.environment.variables.AGENTSPACE_EXTRA_MODULE == "1";
    assert userCfg.home == "/home/${sandboxCfg.user}";
    assert userCfg.createHome;
    assert userCfg.extraGroups == sandboxCfg.groups;
    assert userCfg.openssh.authorizedKeys.keys == [ sshKeys.consumer.publicKey ];
    assert homeCfg.home.username == sandboxCfg.user;
    assert homeCfg.home.homeDirectory == "/home/${sandboxCfg.user}";
    assert homeCfg.home.stateVersion == vmConsumer.config.system.stateVersion;
    assert homeCfg.home.sessionVariables.AGENTSPACE_HM == "1";
    assert homeCfg.programs.home-manager.enable;
    assert homeCfg.programs.git.enable;
    assert builtins.elem pkgs.hello homeCfg.home.packages;
    assert builtins.elem "./id_ed25519" manifest.ssh.exec;
    assert manifest.machine.memory == 512;
    assert manifest.machine.vcpu == 16;
    assert builtins.any (volume: volume.image == "/var/lib/agentspace/home.img") manifest.volumes;
    assert builtins.any (
      volume: volume.image == "/var/lib/agentspace/nix-store-overlay.img"
    ) manifest.volumes;
    true;
in
{
  sandbox-consumer-workflow =
    assert consumerWorkflow;
    pkgs.runCommand "sandbox-consumer-workflow" { } ''
      grep -F 'virtie launch -v --ssh --manifest=' ${launchScript}
      grep -F ${pkgs.lib.escapeShellArg manifestPath} ${launchScript}
      grep -F ${pkgs.lib.escapeShellArg manifestTemplate} ${launchScript}
      grep -F 'mkdir -p "$(' ${launchScript}
      grep -F 'rm -f "$MANIFEST_PATH"' ${launchScript}
      grep -F 'install -m 0644 ${pkgs.lib.escapeShellArg manifestTemplate} "$MANIFEST_PATH"' ${launchScript}
      grep -F 'nix path-info --closure-size --human-readable "$SYSTEM_CLOSURE"' ${launchScript}
      grep -F 'mkSandbox closure size:' ${launchScript}
      test ${pkgs.lib.escapeShellArg manifestPath} = '.agentspace/virtie-agent-sandbox.json'
      grep -F "bash -lc pwd" ${launchScript}
      grep -F 'launch -v --ssh --manifest="$MANIFEST_PATH" -- "$@"' ${launchScript}

      grep -F 'managed by virtie' ${virtiofsdHelper}

      touch $out
    '';

  sandbox-extra-modules-reject-fixed-vsock-cid =
    assert !unsupportedFixedCID.success;
    pkgs.runCommand "sandbox-extra-modules-reject-fixed-vsock-cid" { } ''
      touch $out
    '';
}
