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
                workspace.enable = true;

                # Example of overriding defaults via the new options
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

                ID=$(${pkgs.openssl}/bin/openssl rand -hex 6)
                WORKTREE_DIR=".worktrees/agent-$ID"

                echo "🚀 Preparing Agent Environment: $ID"
                echo "📂 Location: $WORKTREE_DIR"

                # Worktree
                mkdir -p .worktrees
                ${pkgs.git}/bin/git worktree add --detach "$WORKTREE_DIR" HEAD

                # Cleanup instructions on exit
                cleanup() {
                  echo "🛑 Agent shutdown."
                  echo "⚠️  Note: Worktree preserved at $WORKTREE_DIR for inspection."
                  echo "   To delete: git worktree remove $WORKTREE_DIR"
                  rm -f ./nix-store-overlay.img
                }
                trap cleanup EXIT

                cd "$WORKTREE_DIR"
                echo "🔨 Building VM..."

                # Add runner script for recursive VMs (a+rx)
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
