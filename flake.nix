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
                protocol = "9p"; # 9p | virtiofs
                connectWith = "console"; # console | ssh

                sshAuthorizedKeys = [
                  # Put your ~/.ssh/id_*.pub here to start an ssh server you can connect to.
                  # Example:
                  # "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPWrZA5SvCSRmewCRj8nKvcZVZz7+Gy7LWV30oZ/MUwr"
                ];

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
          connectScript = mkConnectScript name;
          runnerPath = vmConfig.microvm.declaredRunner.outPath;
          script = pkgs.writeShellScriptBin "launch-agent-${name}" ''
            set -euo pipefail

            REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)
            RUNNER_PATH=${runnerPath}

            ${vmConfig.agentspace.sandbox.initExtra}

            echo "🖥️  Running Agentspace..."
            "$RUNNER_PATH/bin/microvm-run"

          ''
          # FIXME: This is a WIP feature which doesn't work unless we use qemu --daemonize, which we can't do because microvm.nix uses `-chardev stdio`
          + pkgs.lib.optionalString (false) ''
            VM_PID=$!
            trap 'kill "$VM_PID" 2>/dev/null || true; wait "$VM_PID" 2>/dev/null || true' EXIT INT TERM

            echo "🔐 Waiting for SSH over vsock..."
            for _ in $(seq 1 60); do
              exec "${connectScript}/bin/connect-agent-${name}"
              sleep 0.5
            done
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
