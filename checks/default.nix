{
  mkSandbox,
  mkLaunch,
  pkgs,
}:
let
  args = {
    inherit mkLaunch mkSandbox pkgs;
  };
in
import ./virtie-launch.nix args
// import ./virtie-e2e.nix args
// import ./extra-modules.nix args
// import ./home-manager.nix args
// import ./unsupported-paths.nix args
