{
  description = "Example agent flake using agentspace.lib.mkSandbox";

  inputs = {
    agentspace.url = "path:../..";
    nixpkgs.follows = "agentspace/nixpkgs";
    microvm.follows = "agentspace/microvm";
  };

  outputs =
    {
      self,
      agentspace,
      ...
    }:
    let
      system = "x86_64-linux";
      mkExampleSandbox =
        args:
        import ./sandbox.nix (
          {
            mkSandbox = agentspace.lib.mkSandbox;
          }
          // args
        );
    in
    rec {
      lib.mkExampleSandbox = mkExampleSandbox;

      nixosConfigurations.agentspace = self.lib.mkExampleSandbox { };

      apps.${system} = {
        default = self.apps.${system}.launch;
        launch = {
          type = "app";
          program = agentspace.lib.mkLaunch self.nixosConfigurations.agentspace;
        };
        connect = {
          type = "app";
          program = agentspace.lib.mkConnect self.nixosConfigurations.agentspace;
        };
      };
    };
}
