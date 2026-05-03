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
        alpine = {
          enable = true;
          rootDiskBuilder = agentspace.lib.mkAlpineRootDisk { };
        };

        ssh.authorizedKeys = [
          "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPWrZA5SvCSRmewCRj8nKvcZVZz7+Gy7LWV30oZ/MUwr shazowic-fae
"
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
