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
                # airlock.enable = true;

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


      checks.${system} =
        let
          vmAirlock = nixpkgs.lib.nixosSystem {
            inherit system;
            modules = [
              microvm.nixosModules.microvm
              ./sandbox-qemu.nix
              ./airlock.nix
              {
                agentspace.sandbox = {
                  enable = true;
                  user = "agent";
                  hostName = "agent-sandbox-airlock";
                  protocol = "9p";
                  airlock.enable = true;

                  persistence.homeImage = "./home.img";
                  bundle = [ ];
                };

                nix.registry.nixpkgs.flake = nixpkgs;
                nix.nixPath = [ "nixpkgs=${nixpkgs}" ];
                nix.settings.experimental-features = [
                  "nix-command"
                  "flakes"
                ];
              }
            ];
          };

          airlockLaunchScript =
            let
              vmConfig = vmAirlock.config;
              runnerPath = vmConfig.microvm.declaredRunner.outPath;
              script = pkgs.writeShellScriptBin "launch-agent-airlock" ''
                set -e

                REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)

${vmConfig.agentspace.sandbox.initExtra}

                echo "🖥️  Running Agent..."
                exec "${runnerPath}/bin/microvm-run"
              '';
            in
            "${script}/bin/launch-agent-airlock";
        in
        {
          launch-agent-airlock-init = pkgs.runCommand "launch-agent-airlock-init" { } ''
            grep -F 'AGENT_ID=' ${airlockLaunchScript}
            grep -F 'trap cleanup EXIT' ${airlockLaunchScript}
            grep -F 'cd "$AGENT_DIR"' ${airlockLaunchScript}
            grep -F 'if [ "0" = "1" ]; then' ${airlockLaunchScript}

            touch $out
          '';
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
