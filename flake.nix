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
      lib = nixpkgs.lib;
      defaultHostSystem = "x86_64-linux";
      supportedHostSystems = [
        "x86_64-linux"
        "aarch64-darwin"
      ];
      defaultGuestSystemFor =
        hostSystem: if hostSystem == "aarch64-darwin" then "aarch64-linux" else "x86_64-linux";
      forHostSystems = lib.genAttrs supportedHostSystems;
      pkgsFor = system: nixpkgs.legacyPackages.${system};
      mkVirtiePackage =
        pkgs:
        pkgs.buildGoModule {
          pname = "virtie";
          version = "0.1.0";
          src = ./virtie;
          vendorHash = "sha256-FOHUyDHCB1nuf5XO5vPHbJhthBbObhZBZ2xzY94O7ts=";
          subPackages = [ "." ];
          env.CGO_ENABLED = 0;
        };

      mkSandbox =
        cfg:
        let
          hostSystem = cfg.hostSystem or defaultHostSystem;
          guestSystem = cfg.guestSystem or defaultGuestSystemFor hostSystem;
          hostPkgs = pkgsFor hostSystem;
          extraModules = cfg.extraModules or [ ];
          sandboxCfg = builtins.removeAttrs cfg [
            "extraModules"
            "hostSystem"
            "guestSystem"
          ];
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
              }
              // sandboxCfg;

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
            system = guestSystem;
            specialArgs = {
              inherit hostPkgs hostSystem guestSystem;
            };
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

      mkLaunch = nixosConfig: mkLaunchFor defaultHostSystem nixosConfig;

      mkLaunchFor =
        hostSystem: nixosConfig:
        let
          pkgs = pkgsFor hostSystem;
          virtiePackage = mkVirtiePackage pkgs;
          vmConfig = nixosConfig.config;
          launchCfg = vmConfig.agentspace.sandbox.launch;
          sshCfg = vmConfig.agentspace.sandbox.ssh or { };
          remoteCommand = sshCfg.command or "";
          sshAutoconnect = sshCfg.autoconnect or true;
          systemClosure = vmConfig.system.build.toplevel or "";
          script = pkgs.writeShellScriptBin "launch-agent" ''
            set -euo pipefail

            REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)

            ${launchCfg.commonInit}

            MANIFEST_PATH=${lib.escapeShellArg launchCfg.virtieManifest}
            ${pkgs.coreutils}/bin/mkdir -p "$(${pkgs.coreutils}/bin/dirname "$MANIFEST_PATH")"
            ${pkgs.coreutils}/bin/rm -f "$MANIFEST_PATH"
            ${pkgs.coreutils}/bin/install -m 0644 ${lib.escapeShellArg launchCfg.virtieManifestTemplate} "$MANIFEST_PATH"

            SYSTEM_CLOSURE=${lib.escapeShellArg systemClosure}
            if [ -n "$SYSTEM_CLOSURE" ] && closure_info="$(${pkgs.nix}/bin/nix path-info --closure-size --human-readable "$SYSTEM_CLOSURE" 2>/dev/null)"; then
              closure_path=
              closure_size=
              closure_unit=
              read -r closure_path closure_size closure_unit <<< "$closure_info"
              if [ -n "$closure_size" ]; then
                if [ -n "$closure_unit" ]; then
                  closure_size="$closure_size $closure_unit"
                fi
                echo "📦 mkSandbox closure size: $closure_size"
              fi
            elif [ -n "$SYSTEM_CLOSURE" ]; then
              echo "mkSandbox closure size unavailable for $SYSTEM_CLOSURE" >&2
            fi

            if [ "$#" -eq 0 ] && [ -n ${lib.escapeShellArg remoteCommand} ]; then
              exec ${virtiePackage}/bin/virtie launch -v --ssh --manifest="$MANIFEST_PATH" -- ${lib.escapeShellArg remoteCommand}
            fi

            if [ "$#" -eq 0 ]; then
              ${
                if sshAutoconnect then
                  ''
                    exec ${virtiePackage}/bin/virtie launch -v --ssh --manifest="$MANIFEST_PATH"
                  ''
                else
                  ''
                    exec ${virtiePackage}/bin/virtie launch -v --manifest="$MANIFEST_PATH"
                  ''
              }
            fi

            exec ${virtiePackage}/bin/virtie launch -v --ssh --manifest="$MANIFEST_PATH" -- "$@"
          '';
        in
        "${script}/bin/launch-agent";

      vmConfigs = {
        default = mkSandbox { };
      };
      defaultPkgs = pkgsFor defaultHostSystem;
      defaultVirtiePackage = mkVirtiePackage defaultPkgs;
      checkArgs = {
        inherit mkLaunch mkSandbox;
        pkgs = defaultPkgs;
        virtiePackage = defaultVirtiePackage;
      };
    in
    {
      nixosConfigurations = vmConfigs;

      packages = forHostSystems (hostSystem: {
        virtie = mkVirtiePackage (pkgsFor hostSystem);
      });

      lib = {
        inherit
          mkSandbox
          mkLaunch
          mkLaunchFor
          defaultGuestSystemFor
          ;
      };

      checks.${defaultHostSystem} = import ./checks {
        inherit (checkArgs)
          mkLaunch
          mkSandbox
          pkgs
          virtiePackage
          ;
      };

      legacyPackages.${defaultHostSystem}.graphicalChecks = import ./checks/graphical.nix checkArgs;

      apps = forHostSystems (hostSystem: {
        default = self.apps.${hostSystem}.launch;
        launch = {
          type = "app";
          program = mkLaunchFor hostSystem (mkSandbox {
            inherit hostSystem;
          });
        };
      });
    };
}
