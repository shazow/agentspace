{
  description = "Agent Sandbox";

  inputs.microvm = {
    url = "github:astro/microvm.nix";
    inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    {
      self,
      nixpkgs,
      microvm,
    }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
    in
    {
      nixosConfigurations = {
        vm = nixpkgs.lib.nixosSystem {
          inherit system;
          modules = [
            microvm.nixosModules.microvm
            ./sandbox-qemu.nix
            ./airlock.nix

            # Module Configuration
            {
              agentspace.sandbox = {
                enable = true;
                user = "agent";
                hostName = "agent-sandbox";
                protocol = "9p";
                airlock.enable = false;

                persistence.homeImage = "./home.img";
                bundle = [ ];
              };

              # System-specific overrides can still go here
              nix.registry.nixpkgs.flake = nixpkgs;
              nix.nixPath = [ "nixpkgs=${nixpkgs}" ];
              nix.settings.experimental-features = [
                "nix-command"
                "flakes"
              ];
            }
          ];
        };
      };

      packages.${system} =
        let
          runner = self.nixosConfigurations.vm.config.microvm.declaredRunner;
        in
        {
          default = runner;
          vm = runner;
        };

      apps.${system} = {
        default = self.apps.${system}.launch;
        launch = {
          type = "app";
          program =
            let
              # Access config to make script dynamic based on module settings
              vmConfig = self.nixosConfigurations.vm.config;
              runnerPath = vmConfig.microvm.declaredRunner.outPath;

              script = pkgs.writeShellScriptBin "launch-agent" ''
                set -e

                REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)
${vmConfig.agentspace.sandbox.airlock.launchAgentSetup}

                echo "🖥️  Running Agent..."
                exec "${runnerPath}/bin/microvm-run"
              '';
            in
            "${script}/bin/launch-agent";
        };
      };
    };
}
