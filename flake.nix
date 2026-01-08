{
  description = "Agent Sandbox";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        
        # --- 0. CONSTANTS ---
        # Change these to customize the container user
        containerUser = "node";
        containerHome = "/home/${containerUser}";

        # --- 1. CONFIGURATION ---
        mounts = [
          { type="bind";   src=''"$(pwd)"'';             target="/workspace";            opts="rw"; }
          { type="volume"; src="commandhistory";         target="/commandhistory";       opts=""; }
          # Persist config between sessions (directories auto-created by homeMountCmds below)
          #{ type="volume"; src="claude-code-config";     target="${containerHome}/.claude";    opts=""; }
          #{ type="volume"; src="codex-config";           target="${containerHome}/.codex";     opts=""; }
          #{ type="volume"; src="gh-config";              target="${containerHome}/.config/gh"; opts=""; }
        ];

        mountFlags = pkgs.lib.concatMapStringsSep " " (m: 
          "--mount type=${m.type},source=${m.src},target=${m.target}" 
          + (if m.opts != "" then ",${m.opts}" else "")
        ) mounts;

        # Generate mkdir commands for any mount targeting the container home
        # This ensures the mount points exist with correct user permissions (700)
        homeMountCmds = pkgs.lib.concatMapStringsSep "\n" (m:
          if pkgs.lib.hasPrefix "${containerHome}/" m.target then
            "mkdir -p -m 700 .${m.target}"
          else
            ""
        ) mounts;

        # --- 2. IMAGE COMPONENTS ---
        # IMPROVED POLICY:
        # Instead of "insecureAcceptAnything" for everything, we Reject by default.
        # We only allow "docker-archive" (tarballs), which is what 'podman load' uses
        # when loading the local Nix-built image.
        policyConf = (pkgs.formats.json {}).generate "policy.json" {
          default = [ { type = "reject"; } ];
          transports = {
            docker-archive = { "" = [ { type = "insecureAcceptAnything"; } ]; };
            oci-archive = { "" = [ { type = "insecureAcceptAnything"; } ]; };
            dir = { "" = [ { type = "insecureAcceptAnything"; } ]; };
          };
        };

        userInfo = pkgs.runCommand "user-info" {} ''
          mkdir -p $out/etc
          echo "${containerUser}:x:1000:1000::${containerHome}:/bin/bash" > $out/etc/passwd
          echo "${containerUser}:x:1000:" > $out/etc/group
        '';

        agentImage = pkgs.dockerTools.buildLayeredImage {
          name = "agent-sandbox-image";
          tag = "latest";
          
          contents = [ userInfo ] ++ (with pkgs; [
            coreutils
            bashInteractive
            git
            gnugrep
            curl
            which
            gh
            dockerTools.caCertificates 
            fish 
            nodejs_22
            python3
            uv
          ]);

          config = {
            User = containerUser;
            WorkingDir = "/workspace";
            Env = [
              "NODE_OPTIONS=--max-old-space-size=4096"
              # Enable global npm installs for the non-root user
              "NPM_CONFIG_PREFIX=${containerHome}/.npm-global"
              "PATH=${containerHome}/.npm-global/bin:/bin:/usr/bin:/usr/local/bin"
              # Common config paths
              "CLAUDE_CONFIG_DIR=${containerHome}/.claude"
              "CODEX_HOME=${containerHome}/.codex"
            ];
            Cmd = [ "fish" ];
          };

          fakeRootCommands = ''
            mkdir -p -m 1777 tmp
            mkdir -p -m 777 workspace
            # Create user home structure and enable npm globals
            # We use .${containerHome} to create the directory path relative to the image root
            mkdir -p -m 755 .${containerHome}/.npm-global
            ${homeMountCmds}
            chown -R 1000:1000 .${containerHome}
          '';
        };

        # --- 3. RUNNER SCRIPT ---
        runScript = pkgs.writeShellApplication {
          name = "run-agent";
          runtimeInputs = [ pkgs.podman pkgs.slirp4netns ];
          
          text = ''
            IMAGE_ARCHIVE="${agentImage}"
            
            # Define cleanup function to run on exit
            cleanup() {
              echo ""
              echo "--- Cleaning up Image ---"
              # Remove the image loaded by this session
              podman image rm "agent-sandbox-image:latest" >/dev/null 2>&1 || true
            }
            # Register the trap to run on EXIT (clean or error) and INT (Ctrl+C)
            trap cleanup EXIT INT TERM

            echo "--- Loading Image from Nix Store ---"
            # Load the image (this will tag it as agent-sandbox-image:latest)
            podman load --quiet --signature-policy=${policyConf} --input "$IMAGE_ARCHIVE"

            echo "--- Launching Sandbox (runsc) ---"
            
            # Pass API keys if they exist in the host environment
            ENV_FLAGS=""
            [ -n "''${GEMINI_API_KEY:-}" ] && ENV_FLAGS="$ENV_FLAGS --env GEMINI_API_KEY"
            [ -n "''${ANTHROPIC_API_KEY:-}" ] && ENV_FLAGS="$ENV_FLAGS --env ANTHROPIC_API_KEY"
            [ -n "''${OPENAI_API_KEY:-}" ] && ENV_FLAGS="$ENV_FLAGS --env OPENAI_API_KEY"

            # shellcheck disable=SC2086 
            # REMOVED 'exec' so the script stays alive to run the trap after podman exits
            podman run -it --rm \
              --name agent-sandbox-instance \
              --runtime=${pkgs.gvisor}/bin/runsc \
              --runtime-flag=ignore-cgroups \
              --cgroup-manager=cgroupfs \
              --events-backend=file \
              --network=slirp4netns \
              --userns=keep-id \
              --workdir /workspace \
              $ENV_FLAGS \
              ${mountFlags} \
              agent-sandbox-image:latest \
              fish
          '';
        };

      in {
        packages.image = agentImage;
        packages.default = runScript;
        
        devShells.default = pkgs.mkShell {
          packages = [ runScript ];
          shellHook = ''
            echo "Agent Sandbox Environment"
            exec run-agent
          '';
        };
      }
    );
}
