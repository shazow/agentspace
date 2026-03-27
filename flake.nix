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
                consoleLogin.enable = false;
                sshLogin.enable = true;
                # Example: explicitly wire a host public key into the guest image.
                # sshLogin.authorizedKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... you@example";
                # Example (pure, tracked file): sshLogin.authorizedKey = builtins.readFile ./keys/agent.pub;
                # Example (impure): sshLogin.authorizedKey = builtins.readFile ~/.ssh/id_ed25519.pub;

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

          # FIXME: This assumes the guest SSH daemon listens on the default port.
          exec ${pkgs.openssh}/bin/ssh \
            -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null \
            -o GlobalKnownHostsFile=/dev/null \
            # Assumes systemd-ssh-proxy support is available via ssh_config.
            # Fallback (if unavailable):
            # -o ProxyCommand='${pkgs.socat}/bin/socat STDIO VSOCK-CONNECT:${toString cid}:22'
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

            echo "🖥️  Running Agent..."
            if [ "${toString vmConfig.agentspace.sandbox.sshLogin.enable}" = "true" ]; then
              "$RUNNER_PATH/bin/microvm-run" &
              VM_PID=$!
              trap 'kill "$VM_PID" 2>/dev/null || true; wait "$VM_PID" 2>/dev/null || true' EXIT INT TERM

              echo "🔐 Waiting for SSH over vsock..."
              for _ in $(seq 1 60); do
                if "${connectScript}/bin/connect-agent-${name}" true >/dev/null 2>&1; then
                  exec "${connectScript}/bin/connect-agent-${name}"
                fi
                sleep 0.5
              done

              echo "Timed out waiting for SSH to become ready." >&2
              exit 1
            else
              "$RUNNER_PATH/bin/microvm-run"
            fi
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
