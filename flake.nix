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

      mkSandbox = { extraModules ? [] }: nixpkgs.lib.nixosSystem {
        inherit system;
        modules = [
          microvm.nixosModules.microvm
          ./sandbox-qemu.nix

          # Module Configuration
          {
            agentspace.sandbox = {
              enable = true;
              user = "agent";
              hostName = "agent-sandbox";
              protocol = "9p";

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
        ] ++ extraModules;
      };
    in
    {
      nixosConfigurations = {
        vm = mkSandbox {};
        vmWithAirlock = mkSandbox {
          extraModules = [
            ./airlock.nix
            {
              agentspace.sandbox.airlock.enable = true;
            }
          ];
        };
      };

      packages.${system} =
        let
          mkRunner = name: self.nixosConfigurations.${name}.config.microvm.declaredRunner;
        in
        {
          default = mkRunner "vm";
          vm = mkRunner "vm";
          vmWithAirlock = mkRunner "vmWithAirlock";
        };

      checks.${system} = import ./checks.nix {
        inherit
          microvm
          nixpkgs
          pkgs
          system
          ;
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

                ${vmConfig.agentspace.sandbox.initExtra}

                echo "🖥️  Running Agent..."
                exec "${runnerPath}/bin/microvm-run"
              '';
            in
            "${script}/bin/launch-agent";
        };
      };
    };
}
