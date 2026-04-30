{
  mkSandbox,
  mkLaunch,
  pkgs,
  virtiePackage,
}:
let
  args = {
    inherit mkLaunch mkSandbox pkgs virtiePackage;
  };
in
import ./virtie-manifest.nix args
// import ./virtie-e2e.nix args
// import ./consumer-workflow.nix args
