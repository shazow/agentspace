{
  description = "Example virtie-backed agent flake using agentspace.lib.mkSandbox";

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
        extraModules = [
          {
            # Keep the example launchable without requiring permission to chgrp
            # virtiofs sockets to the host's kvm group.
            microvm.virtiofsd.group = null;
          }
        ];

        # Document the currently supported end-to-end launch path explicitly.
        connectWith = "ssh";
        protocol = "virtiofs";

        # Point the launcher at the matching private key on the host.
        sshIdentityFile = "./id_ed25519";
        sshAuthorizedKeys = [
          # Matches the example-local keypair in this directory.
          "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHKb94TYnrM5gcFsQIL9FE6qxjjZSmehVOGCnfv+E8r/ agentspace-example"
        ];

        # Keep the example ephemeral aside from the writable nix store overlay.
        persistence.homeImage = null;
      };
      launch = agentspace.lib.mkLaunch sandbox;
      connect = agentspace.lib.mkConnect sandbox;
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
        connect = {
          type = "app";
          program = connect;
        };
      };
    };
}
