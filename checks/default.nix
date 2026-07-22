{
  mkSandbox,
  mkLaunch,
  mkExecSSH,
  pkgs,
  virtlePackage,
}:
let
  args = {
    inherit
      mkLaunch
      mkSandbox
      mkExecSSH
      pkgs
      virtlePackage
      ;
  };
in
import ./virtle-manifest.nix args
// import ./virtle-e2e.nix args
// import ./consumer-workflow.nix args
