{
  description = "Example virtie-backed Alpine agent flake using agentspace.lib.mkSandbox";

  inputs = {
    agentspace.url = "path:../..";
    home-manager.follows = "agentspace/home-manager";
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
      sandbox = agentspace.lib.mkSandbox {
        alpine = true;

        # Point the launcher at the matching private key on the host.
        ssh.identityFile = "./id_ed25519";
        ssh.authorizedKeys = [
          # Matches the example-local keypair in this directory.
          "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ1epknKnIIbdZY58XHUp4ZFfrvWZ9WO/eJnz5Vvxf+l agentspace-alpine-example"
        ];
      };
      launch = agentspace.lib.mkLaunch sandbox;
    in
    {
      nixosConfigurations.agentspace = sandbox;

      apps.${system} = {
        default = {
          type = "app";
          program = launch;
        };
        launch = {
          type = "app";
          program = launch;
        };
      };
    };
}
