{
  mkSandbox,
  pkgs,
  ...
}:
let
  vmExtraModules = mkSandbox {
    extraModules = [
      {
        agentspace.sandbox.extraModules = [
          {
            microvm.mem = 512;
            microvm.vsock.cid = 42;
          }
        ];
      }
    ];
  };

  sandboxCfg = vmExtraModules.config.agentspace.sandbox;

  _ =
    assert builtins.length sandboxCfg.extraModules == 1;
    assert vmExtraModules.config.microvm.mem == 512;
    assert vmExtraModules.config.microvm.vsock.cid == 42;
    true;
in
{
  sandbox-extra-modules = pkgs.runCommand "sandbox-extra-modules" { } ''
    touch $out
  '';
}
