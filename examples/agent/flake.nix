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
    in rec
    {
      nixosConfigurations.agentspace = agentspace.lib.mkSandbox {
        connectWith = "ssh";
        protocol = "virtiofs";
        sshAuthorizedKeys = [
          # Replace with your own key.
          "ssh-ed25519 AAAA... user@example"
        ];
      };

      apps.${system}.default = {
        type = "app";
        program = agentspace.lib.mkLaunch nixosConfigurations.agentspace;
      };
    };
}
