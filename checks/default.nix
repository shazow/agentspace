{
  mkSandbox,
  mkLaunch,
  mkExecSSH,
  pkgs,
  virtiePackage,
}:
let
  args = {
    inherit
      mkLaunch
      mkSandbox
      mkExecSSH
      pkgs
      virtiePackage
      ;
  };
in
import ./virtie-manifest.nix args
// import ./virtie-e2e.nix args
// import ./consumer-workflow.nix args
