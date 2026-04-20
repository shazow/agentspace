{
  description = "Agent Sandbox";

  inputs.home-manager = {
    url = "github:nix-community/home-manager";
    inputs.nixpkgs.follows = "nixpkgs";
  };

  inputs.microvm = {
    url = "github:astro/microvm.nix";
    inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    {
      self,
      home-manager,
      nixpkgs,
      microvm,
    }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      lib = nixpkgs.lib;
      virtiePackage = pkgs.buildGoModule {
        pname = "virtie";
        version = "0.1.0";
        src = ./virtie;
        vendorHash = "sha256-Sz55B1Wk5ONiclesyflNat57g8taqlg/Iqd9t/KOvik=";
        subPackages = [ "cmd/virtie" ];
        env.CGO_ENABLED = 0;
      };

      mkSandbox =
        cfg:
        let
          extraModules = cfg.extraModules or [ ];
          sandboxCfg = builtins.removeAttrs cfg [ "extraModules" ];
          baseModules = [
            microvm.nixosModules.microvm
            home-manager.nixosModules.home-manager
            ./sandbox-qemu.nix
          ]
          ++ extraModules
          ++ [
            # Module Configuration
            {
              agentspace.sandbox = {
                enable = true;
              } // sandboxCfg;

              # System-specific overrides can still go here
              system.stateVersion = "25.11";
              nix.registry.nixpkgs.flake = nixpkgs;
              nix.nixPath = [ "nixpkgs=${nixpkgs}" ];
              nix.settings.experimental-features = [
                "nix-command"
                "flakes"
              ];
            }
          ];
          baseSystem = nixpkgs.lib.nixosSystem {
            inherit system;
            modules = baseModules;
          };
          sandboxExtraModules = baseSystem.config.agentspace.sandbox.extraModules;
        in
        if sandboxExtraModules == [ ] then
          baseSystem
        else
          baseSystem.extendModules {
            modules = sandboxExtraModules;
          };

      mkConnect =
        nixosConfig:
        let
          vmConfig = nixosConfig.config;
          script = pkgs.writeShellScriptBin "connect-agent" ''
            set -euo pipefail

            echo "connect-agent is unsupported when virtie allocates vsock CIDs at launch time." >&2
            echo "Start a fresh session with launch-agent instead." >&2
            exit 1
          '';
        in
        "${script}/bin/connect-agent";

      mkLaunch =
        nixosConfig:
        let
          vmConfig = nixosConfig.config;
          launchCfg = vmConfig.agentspace.sandbox.launch;
          script = pkgs.writeShellScriptBin "launch-agent" ''
            set -euo pipefail

            REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)

            ${launchCfg.commonInit}

            exec ${virtiePackage}/bin/virtie launch ${launchCfg.virtieManifest} -- "$@"
          '';
        in
        "${script}/bin/launch-agent";

      vmConfigs = {
        default = mkSandbox { };
      };
    in
    {
      nixosConfigurations = vmConfigs;

      packages.${system} = {
        default = mkLaunch vmConfigs.default;
        virtie = virtiePackage;
        vm = mkLaunch vmConfigs.default;
        connect = mkConnect vmConfigs.default;
      };

      lib = {
        inherit mkSandbox mkLaunch mkConnect;
      };

      checks.${system} = import ./checks {
        inherit mkConnect mkLaunch mkSandbox pkgs;
      };

      apps.${system} = {
        default = self.apps.${system}.launch;
        launch = {
          type = "app";
          program = mkLaunch vmConfigs.default;
        };
        connect = {
          type = "app";
          program = mkConnect vmConfigs.default;
        };
      };
    };
}
