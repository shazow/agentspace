{
  mkLaunch,
  mkSandbox,
  pkgs,
  ...
}:
let
  consumerPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGUQ2FsZrmb4kVgX9X6N1Llqfu6N7o8gBC4M0coYv0Ab agentspace-consumer-test";

  vmConsumer = mkSandbox {
    sshAuthorizedKeys = [ consumerPublicKey ];
    sshIdentityFile = "./id_ed25519";
    persistence = {
      homeImage = "/var/lib/agentspace/home.img";
      storeOverlay = "/var/lib/agentspace/nix-store-overlay.img";
    };
    extraModules = [
      {
        microvm.vcpu = 16;
        microvm.mem = 8 * 1024;
      }
    ];
    homeModules = [
      (
        { pkgs, ... }:
        {
          home.packages = [
            pkgs.go
            pkgs.just
            pkgs.nodejs
          ];
          programs.git.enable = true;
        }
      )
    ];
  };

  sandboxCfg = vmConsumer.config.agentspace.sandbox;
  userCfg = vmConsumer.config.users.users.${sandboxCfg.user};
  homeCfg = vmConsumer.config.home-manager.users.${sandboxCfg.user};
  manifest = sandboxCfg.launch.virtieManifest;
  launchScript = mkLaunch vmConsumer;

  _ =
    assert sandboxCfg.sshIdentityFile == "./id_ed25519";
    assert sandboxCfg.persistence.homeImage == "/var/lib/agentspace/home.img";
    assert sandboxCfg.persistence.storeOverlay == "/var/lib/agentspace/nix-store-overlay.img";
    assert vmConsumer.config.microvm.vcpu == 16;
    assert vmConsumer.config.microvm.mem == 8 * 1024;
    assert userCfg.openssh.authorizedKeys.keys == [ consumerPublicKey ];
    assert homeCfg.programs.git.enable;
    assert builtins.length homeCfg.home.packages == 3;
    true;
in
{
  sandbox-consumer-surface = pkgs.runCommand "sandbox-consumer-surface" { } ''
    grep -F 'virtie launch' ${launchScript}
    grep -F '"./id_ed25519"' ${manifest}
    grep -F '"/var/lib/agentspace/home.img"' ${manifest}
    grep -F '"/var/lib/agentspace/nix-store-overlay.img"' ${manifest}

    touch $out
  '';
}
