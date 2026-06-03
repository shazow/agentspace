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
        vendorHash = "sha256-QbBNPf4BYJ2T6YOlCXk3CWu0tY+6f7TgbkZcsl4LMAQ=";
        subPackages = [ "." ];
        env.CGO_ENABLED = 0;
      };

      mkExecSSH = import ./lib/mkExecSSH.nix { inherit pkgs lib; };
      mkVirtioFSD = import ./lib/mkVirtioFSD.nix { inherit pkgs lib; };

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
            inherit system;
            modules = baseModules;
            specialArgs = { inherit mkExecSSH mkVirtioFSD; };
          };
          sandboxExtraModules = baseSystem.config.agentspace.sandbox.extraModules;
        in
        if sandboxExtraModules == [ ] then
          baseSystem
        else
          baseSystem.extendModules {
            modules = sandboxExtraModules;
            specialArgs = { inherit mkExecSSH mkVirtioFSD; };
          };

      mkLaunch =
        nixosConfig:
        let
          vmConfig = nixosConfig.config;
          launchCfg = vmConfig.agentspace.sandbox.launch;
          sshCfg = vmConfig.agentspace.sandbox.ssh or { };
          remoteCommand = sshCfg.command or "";
          sshAutoconnect = sshCfg.autoconnect or true;
          systemClosure = vmConfig.system.build.toplevel or null;
          script = pkgs.writeShellScriptBin "launch-agent" ''
            set -euo pipefail

            REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)

            ${launchCfg.commonInit}

            MANIFEST_PATH=${lib.escapeShellArg launchCfg.virtieManifest}
            ${pkgs.coreutils}/bin/mkdir -p "$(${pkgs.coreutils}/bin/dirname "$MANIFEST_PATH")"
            ${pkgs.coreutils}/bin/rm -f "$MANIFEST_PATH"
            ${pkgs.coreutils}/bin/install -m 0644 ${lib.escapeShellArg launchCfg.virtieManifestTemplate} "$MANIFEST_PATH"

            ${lib.optionalString (systemClosure != null) ''
              SYSTEM_CLOSURE=${lib.escapeShellArg systemClosure}
              if closure_info="$(${pkgs.nix}/bin/nix path-info --closure-size --human-readable "$SYSTEM_CLOSURE" 2>/dev/null)"; then
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
              else
                echo "mkSandbox closure size unavailable for $SYSTEM_CLOSURE" >&2
              fi
            ''}

            if [ "$#" -eq 0 ] && [ -n ${lib.escapeShellArg remoteCommand} ]; then
              exec ${virtiePackage}/bin/virtie --manifest="$MANIFEST_PATH" launch -v --ssh -- ${lib.escapeShellArg remoteCommand}
            fi

            if [ "$#" -eq 0 ]; then
              ${
                if sshAutoconnect then
                  ''
                    exec ${virtiePackage}/bin/virtie --manifest="$MANIFEST_PATH" launch -v --ssh
                  ''
                else
                  ''
                    exec ${virtiePackage}/bin/virtie --manifest="$MANIFEST_PATH" launch -v
                  ''
              }
            fi

            exec ${virtiePackage}/bin/virtie --manifest="$MANIFEST_PATH" launch -v --ssh -- "$@"
          '';
        in
        "${script}/bin/launch-agent";

      vmConfigs = {
        default = mkSandbox { };
      };
      checkArgs = {
        inherit
          mkLaunch
          mkSandbox
          mkExecSSH
          pkgs
          virtiePackage
          ;
      };

    in
    {
      nixosConfigurations = vmConfigs;

      packages.${system} = {
        virtie = virtiePackage;
      };

      formatter.${system} = pkgs.nixfmt-tree;

      nixosModules.default = ./sandbox-qemu.nix;

      lib = {
        inherit
          mkSandbox
          mkLaunch
          mkExecSSH
          mkVirtioFSD
          ;
      };

      checks.${system} = import ./checks {
        inherit (checkArgs)
          mkLaunch
          mkSandbox
          mkExecSSH
          pkgs
          virtiePackage
          ;
      };

      legacyPackages.${system}.graphicalChecks = import ./checks/graphical.nix checkArgs;

      apps.${system} = {
        default = self.apps.${system}.launch;
        launch = {
          type = "app";
          program = mkLaunch vmConfigs.default;
        };
      };
    };
}
