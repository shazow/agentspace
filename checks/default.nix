{
  mkSandbox,
  mkTinySandbox,
  mkLaunch,
  pkgs,
  virtiePackage,
}:
let
  args = {
    inherit mkLaunch mkSandbox mkTinySandbox pkgs virtiePackage;
  };
in
import ./virtie-manifest.nix args
// import ./tiny-sandbox.nix args
// import ./virtie-e2e.nix args
// import ./consumer-workflow.nix args
