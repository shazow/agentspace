{
  mkLaunch,
  mkSandbox,
  pkgs,
  ...
}:
let
  consumerPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGUQ2FsZrmb4kVgX9X6N1Llqfu6N7o8gBC4M0coYv0Ab agentspace-consumer-test";

  vmConsumer = mkSandbox {
    ssh.authorizedKeys = [ consumerPublicKey ];
    ssh.identityFile = "./id_ed25519";
    ssh.command = "bash -lc pwd";
    persistence = {
      homeImage = "/var/lib/agentspace/home.img";
      storeOverlay = "/var/lib/agentspace/nix-store-overlay.img";
    };
    extraModules = [
      {
        microvm.vcpu = 16;
        agentspace.sandbox.extraModules = [
          {
            microvm.mem = 512;
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
    assert sandboxCfg.ssh.identityFile == "./id_ed25519";
    assert sandboxCfg.ssh.command == "bash -lc pwd";
    assert sandboxCfg.ssh.autoconnect == true;
    assert sandboxCfg.persistence.homeImage == "/var/lib/agentspace/home.img";
    assert sandboxCfg.persistence.storeOverlay == "/var/lib/agentspace/nix-store-overlay.img";
    assert builtins.length sandboxCfg.extraModules == 1;
    assert vmConsumer.config.microvm.vcpu == 16;
    assert vmConsumer.config.microvm.mem == 512;
    assert vmConsumer.config.microvm.vsock.cid == null;
    assert userCfg.home == "/home/${sandboxCfg.user}";
    assert userCfg.createHome;
    assert userCfg.openssh.authorizedKeys.keys == [ consumerPublicKey ];
    assert homeCfg.home.username == sandboxCfg.user;
    assert homeCfg.home.homeDirectory == "/home/${sandboxCfg.user}";
    assert homeCfg.home.stateVersion == vmConsumer.config.system.stateVersion;
    assert homeCfg.home.sessionVariables.AGENTSPACE_HM == "1";
    assert homeCfg.programs.home-manager.enable;
    assert homeCfg.programs.git.enable;
    assert builtins.elem pkgs.hello homeCfg.home.packages;
    assert builtins.elem "./id_ed25519" manifest.ssh.argv;
    assert builtins.any (volume: volume.imagePath == "/var/lib/agentspace/home.img") manifest.volumes;
    assert builtins.any (
      volume: volume.imagePath == "/var/lib/agentspace/nix-store-overlay.img"
    ) manifest.volumes;
    true;
in
{
  sandbox-consumer-workflow =
    assert consumerWorkflow;
    pkgs.runCommand "sandbox-consumer-workflow" { } ''
      grep -F 'virtie launch --ssh --manifest=' ${launchScript}
      grep -F ${pkgs.lib.escapeShellArg manifestPath} ${launchScript}
      grep -F ${pkgs.lib.escapeShellArg manifestTemplate} ${launchScript}
      grep -F 'mkdir -p "$(' ${launchScript}
      grep -F 'rm -f "$MANIFEST_PATH"' ${launchScript}
      grep -F 'install -m 0644 ${pkgs.lib.escapeShellArg manifestTemplate} "$MANIFEST_PATH"' ${launchScript}
      test ${pkgs.lib.escapeShellArg manifestPath} = '.agentspace/virtie-agent-sandbox.json'
      grep -F "bash -lc pwd" ${launchScript}
      grep -F 'launch --ssh --manifest="$MANIFEST_PATH" -- "$@"' ${launchScript}
      if grep -F 'systemd-run' ${launchScript} >/dev/null; then
        echo "sandbox-consumer-workflow: unexpected legacy systemd-run in virtie wrapper" >&2
        exit 1
      fi

      grep -F 'managed by virtie' ${virtiofsdHelper}
      if ${pkgs.nix}/bin/nix-store -q --references ${virtiofsdHelper} | grep -E 'supervisor|supervisord' >/dev/null; then
        echo "sandbox-consumer-workflow: unexpected supervisor dependency in virtiofsd helper stub" >&2
        exit 1
      fi

      touch $out
    '';

  sandbox-extra-modules-reject-fixed-vsock-cid =
    assert !unsupportedFixedCID.success;
    pkgs.runCommand "sandbox-extra-modules-reject-fixed-vsock-cid" { } ''
      touch $out
    '';
}
