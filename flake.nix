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
        vendorHash = "sha256-zgrEcEl+75sTcATL7n5gb+hoQguDEnJMIIs0OkuXsuI=";
        subPackages = [ "." ];
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

      mkLaunch =
        nixosConfig:
        let
          vmConfig = nixosConfig.config;
          launchCfg = vmConfig.agentspace.sandbox.launch;
          remoteCommand = vmConfig.agentspace.sandbox.command or "";
          script = pkgs.writeShellScriptBin "launch-agent" ''
            set -euo pipefail

            REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)

            ${launchCfg.commonInit}

            MANIFEST_PATH=${lib.escapeShellArg launchCfg.virtieManifest}
            ${pkgs.coreutils}/bin/mkdir -p "$(${pkgs.coreutils}/bin/dirname "$MANIFEST_PATH")"
            ${pkgs.coreutils}/bin/rm -f "$MANIFEST_PATH"
            ${pkgs.coreutils}/bin/install -m 0644 ${lib.escapeShellArg launchCfg.virtieManifestTemplate} "$MANIFEST_PATH"

            if [ "$#" -eq 0 ] && [ -n ${lib.escapeShellArg remoteCommand} ]; then
              exec ${virtiePackage}/bin/virtie launch --manifest="$MANIFEST_PATH" -- ${lib.escapeShellArg remoteCommand}
            fi

            exec ${virtiePackage}/bin/virtie launch --manifest="$MANIFEST_PATH" -- "$@"
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
        virtie = virtiePackage;
      };

      lib = {
        inherit mkSandbox mkLaunch;
      };

      checks.${system} = import ./checks {
        inherit mkLaunch mkSandbox pkgs virtiePackage;
      };

      apps.${system} = {
        default = self.apps.${system}.launch;
        launch = {
          type = "app";
          program = mkLaunch vmConfigs.default;
        };
      };
    };
}
