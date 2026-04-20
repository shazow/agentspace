{
  mkSandbox,
  mkLaunch,
  pkgs,
}:
let
  args = {
    inherit mkLaunch mkSandbox pkgs;
  };
  manifestChecks = import ./virtie-manifest.nix args;
  launchChecks = import ./virtie-launch.nix args;
in
manifestChecks
// launchChecks
// {
  launch-agent-virtie-balloon-controller-contract = manifestChecks.virtie-manifest-balloon-contract;
}
// import ./virtie-e2e.nix args
// import ./extra-modules.nix args
// import ./home-manager.nix args
// import ./consumer-surface.nix args
