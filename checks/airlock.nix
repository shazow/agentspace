{
  mkSandbox,
  pkgs,
}:
let
  mkLaunchScript = import ./launch-script.nix { inherit pkgs; };

  vmAirlock = mkSandbox {
    extraModules = [
      ../airlock.nix
      {
        agentspace.sandbox = {
          user = "agent";
          hostName = "agent-sandbox-airlock";
          protocol = "9p";
          airlock.enable = true;
          persistence.homeImage = "./home.img";
          bundle = [ ];
        };
      }
    ];
  };

  airlockLaunchScript = mkLaunchScript "launch-agent-airlock" vmAirlock;
in
{
  launch-agent-airlock-init = pkgs.runCommand "launch-agent-airlock-init" { } ''
    grep -F 'AGENT_ID=' ${airlockLaunchScript}
    grep -F 'trap cleanup EXIT' ${airlockLaunchScript}
    grep -F 'cd "$AGENT_DIR"' ${airlockLaunchScript}
    grep -F 'rm -rf "$REPO_DIR/$AGENT_DIR"' ${airlockLaunchScript}

    touch $out
  '';
}
