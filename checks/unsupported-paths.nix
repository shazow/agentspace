{
  mkSandbox,
  pkgs,
  ...
}:
let
  testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBIqXkHFLTDd7n09425txXfdOgJDUb7CpMAdCPVRS94z agentspace-virtie-test";

  unsupportedProtocol = builtins.tryEval (
    (mkSandbox {
      protocol = "9p";
      sshAuthorizedKeys = [ testPublicKey ];
    }).config.system.build.toplevel.drvPath
  );

  unsupportedConsole = builtins.tryEval (
    (mkSandbox {
      connectWith = "console";
      sshAuthorizedKeys = [ testPublicKey ];
    }).config.system.build.toplevel.drvPath
  );

  unsupportedAirlock = builtins.tryEval (
    (mkSandbox {
      sshAuthorizedKeys = [ testPublicKey ];
      extraModules = [
        ../airlock.nix
        {
          agentspace.sandbox.airlock.enable = true;
        }
      ];
    }).config.system.build.toplevel.drvPath
  );

  unsupportedInitExtra = builtins.tryEval (
    (mkSandbox {
      sshAuthorizedKeys = [ testPublicKey ];
      initExtra = ''
        echo "custom launch hook"
      '';
    }).config.system.build.toplevel.drvPath
  );

  _ =
    assert !unsupportedProtocol.success;
    assert !unsupportedConsole.success;
    assert !unsupportedAirlock.success;
    assert !unsupportedInitExtra.success;
    true;
in
{
  unsupported-paths = pkgs.runCommand "unsupported-paths" { } ''
    touch $out
  '';
}
