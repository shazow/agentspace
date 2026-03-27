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
            ./modules/virtiofsd.nix
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
              system.stateVersion = "25.11";
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
            set -euo pipefail

            REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)

            ${vmConfig.agentspace.sandbox.initExtra}

            virtiofsd_runner="${runnerPath}/bin/virtiofsd-run"
            cleanup() {
              if [ -n "''${VIRTIOFSD_PID:-}" ] && kill -0 "$VIRTIOFSD_PID" 2>/dev/null; then
                kill "$VIRTIOFSD_PID" 2>/dev/null || true
                wait "$VIRTIOFSD_PID" 2>/dev/null || true
              fi
            }
            trap cleanup EXIT INT TERM

            if [ -x "$virtiofsd_runner" ]; then
              echo "📦 Starting virtiofsd..."
              "$virtiofsd_runner" &
              VIRTIOFSD_PID=$!
            fi

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
