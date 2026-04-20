{
  mkSandbox,
  pkgs,
  ...
}:
let
  vmHomeManager = mkSandbox {
    homeModules = [
      {
        home.sessionVariables.AGENTSPACE_HM = "1";
      }
    ];
  };

  sandboxCfg = vmHomeManager.config.agentspace.sandbox;
  userCfg = vmHomeManager.config.users.users.${sandboxCfg.user};
  homeCfg = vmHomeManager.config.home-manager.users.${sandboxCfg.user};

  _ =
    assert sandboxCfg.homeModules != [ ];
    assert userCfg.home == "/home/${sandboxCfg.user}";
    assert userCfg.createHome;
    assert homeCfg.home.username == sandboxCfg.user;
    assert homeCfg.home.homeDirectory == "/home/${sandboxCfg.user}";
    assert homeCfg.home.stateVersion == vmHomeManager.config.system.stateVersion;
    assert homeCfg.programs.home-manager.enable;
    assert homeCfg.home.sessionVariables.AGENTSPACE_HM == "1";
    true;
in
{
  sandbox-home-manager = pkgs.runCommand "sandbox-home-manager" { } ''
    touch $out
  '';
}
