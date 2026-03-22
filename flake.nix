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

      mkSandbox =
        {
          extraModules ? [ ],
        }:
        nixpkgs.lib.nixosSystem {
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
                consoleLogin.enable = true;

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
          ]
          ++ extraModules;
        };

      mkLaunchScript =
        name:
        let
          vmConfig = self.nixosConfigurations.${name}.config;
          runnerPath = vmConfig.microvm.declaredRunner.outPath;
          script = pkgs.writeShellScriptBin "launch-agent-${name}" ''
            set -e

            REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)

            ${vmConfig.agentspace.sandbox.initExtra}

            echo "🖥️  Running Agent..."
            "${runnerPath}/bin/microvm-run"
          '';
        in
        script;
    in
    {
      nixosConfigurations = {
        vm = mkSandbox { };
        vmWithAirlock = mkSandbox {
          extraModules = [
            ./airlock.nix
            {
              agentspace.sandbox.airlock.enable = true;
            }
          ];
        };
      };

      packages.${system} = {
        default = mkLaunchScript "vm";
        vm = mkLaunchScript "vm";
        vmWithAirlock = mkLaunchScript "vmWithAirlock";
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
          program = "${mkLaunchScript "vm"}/bin/launch-agent-vm";
        };
      };
    };
}
