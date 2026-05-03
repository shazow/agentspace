{
  mkSandbox,
  mkLaunch,
  mkAlpineRootDisk,
  pkgs,
  virtiePackage,
}:
let
  args = {
    inherit
      mkAlpineRootDisk
      mkLaunch
      mkSandbox
      pkgs
      virtiePackage
      ;
  };
in
import ./virtie-manifest.nix args
// import ./alpine.nix args
// import ./virtie-e2e.nix args
// import ./consumer-workflow.nix args
