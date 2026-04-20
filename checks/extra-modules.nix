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
          }
        ];
      }
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

  sandboxCfg = vmExtraModules.config.agentspace.sandbox;

  _ =
    assert builtins.length sandboxCfg.extraModules == 1;
    assert vmExtraModules.config.microvm.mem == 512;
    assert vmExtraModules.config.microvm.vsock.cid == null;
    assert !unsupportedFixedCID.success;
    true;
in
{
  sandbox-extra-modules = pkgs.runCommand "sandbox-extra-modules" { } ''
    touch $out
  '';
}
