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

            # Module Configuration
            {
              agentspace.sandbox = {
                enable = true;
                user = "agent";
                hostName = "agent-sandbox";
                protocol = "9p";

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
                ID=$(${pkgs.openssl}/bin/openssl rand -hex 6)
                AGENT_DIR=".agentspace/agent-$ID"

                echo "🚀 Preparing Agent Environment: $ID"
                echo "📂 Location: $AGENT_DIR"

                mkdir -p "$AGENT_DIR/inbox" "$AGENT_DIR/outbox"
                ln -sfn "$REPO_DIR" "$AGENT_DIR/inbox/repo"

                cleanup() {
                  echo "🛑 Agent shutdown."
                  rm -f "$REPO_DIR/$AGENT_DIR/inbox/repo"
                  rmdir "$REPO_DIR/$AGENT_DIR/inbox" 2>/dev/null || true
                  if [ -z "$(ls -A "$REPO_DIR/$AGENT_DIR/outbox" 2>/dev/null)" ]; then
                    echo "📭 Outbox empty, cleaning up $AGENT_DIR"
                    rm -rf "$REPO_DIR/$AGENT_DIR"
                  else
                    echo "📬 Outbox has contents, preserving $AGENT_DIR"
                  fi
                }
                trap cleanup EXIT

                cd "$AGENT_DIR"

                install -m 555 <(readlink -f "${runnerPath}/bin/microvm-run") ./runner
                echo "🖥️  Running Agent..."
                ./runner
              '';
            in
            "${script}/bin/launch-agent";
        };
      };
    };
}
