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
        cfg @ { extraModules ? [ ], ... }:
        nixpkgs.lib.nixosSystem {
          inherit system;
          modules = [
            microvm.nixosModules.microvm
            ./modules/virtiofsd.nix
            ./sandbox-qemu.nix

            # Module Configuration
            {
              agentspace.sandbox = cfg;

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

      mkConnectScript =
        name:
        let
          vmConfig = self.nixosConfigurations.${name}.config;
          sandboxCfg = vmConfig.agentspace.sandbox;
          cid = vmConfig.microvm.vsock.cid;
        in
        pkgs.writeShellScriptBin "connect-agent-${name}" ''
          set -euo pipefail

          # Assumes systemd-ssh-proxy support is available via ssh_config.
          # Fallback (if unavailable):
          # -o ProxyCommand='socat STDIO VSOCK-CONNECT:10:22'
          exec ${pkgs.openssh}/bin/ssh \
            -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null \
            -o GlobalKnownHostsFile=/dev/null \
            "${sandboxCfg.user}@vsock/${toString cid}" \
            "$@"
        '';

      mkLaunchScript =
        name:
        let
          vmConfig = self.nixosConfigurations.${name}.config;
          runnerPath = vmConfig.microvm.declaredRunner.outPath;
          script = pkgs.writeShellScriptBin "launch-agent-${name}" ''
            set -euo pipefail

            REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)
            RUNNER_PATH=${runnerPath}

            ${vmConfig.agentspace.sandbox.initExtra}

            echo "🖥️  Running Agentspace..."
            exec "$RUNNER_PATH/bin/microvm-run"
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
        connect = mkConnectScript "vm";
      };

      lib = {
        inherit mkSandbox;
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
        connect = {
          type = "app";
          program = "${mkConnectScript "vm"}/bin/connect-agent-vm";
        };
      };
    };
}
