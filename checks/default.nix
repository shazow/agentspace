{
  mkSandbox,
  pkgs,
}:
let
  args = {
    inherit mkSandbox pkgs;
  };
in
import ./airlock.nix args // import ./ssh-readiness.nix args // import ./example-agent-e2e.nix args
