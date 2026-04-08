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
      mkSandbox = agentspace.lib.mkSandbox;
    in
    {
      nixosConfigurations.demo = mkSandbox {
        extraModules = [
          {
            agentspace.sandbox = {
              hostName = "agent-demo";
              connectWith = "ssh";
              protocol = "virtiofs";
              sshAuthorizedKeys = [
                # Replace with your own key.
                "ssh-ed25519 AAAA... user@example"
              ];
            };
          }
        ];
      };

      packages.${system}.default = mkLaunch self.nixosConfigurations.demo;
    };
}
