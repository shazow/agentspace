{
  description = "Agent Sandbox";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    # Added for VM generation
    nixos-generators = {
      url = "github:nix-community/nixos-generators";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      nixos-generators,
    }:
    let
      mkSandbox =
        {
          pkgs,
          system, # Added system argument for VM generation
          packages ? [ ],
          additionalMounts ? [ ],
          withNix ? true, # Include nix in container
          withNixVolume ? false, # Use a volume to persist /nix across container instances
          withPrivileges ? false, # Reduce security to allow containers inside containers
        }:
        let
          ICON = "🛡️";
          USER = "agent";
          HOME = "/home/${USER}";
          nixVolume = "nix-store-overlay-${builtins.substring 0 12 agentImage.imageDigest}";

          mounts = [
            {
              type = "bind";
              src = ''"$(pwd)"'';
              target = "/workspace";
              opts = "rw";
            }
            {
              type = "volume";
              src = "home";
              target = HOME;
              opts = "rw";
            }
          ]
          ++ (pkgs.lib.optionals withNixVolume [
            # Persist Nix store and database.
            {
              type = "volume";
              src = "${nixVolume}";
              target = "/nix";
              opts = "";
            }
          ])
          ++ additionalMounts;

          mountFlags = pkgs.lib.concatMapStringsSep " " (
            m:
            if m.type == "podman-overlay" then
              "-v ${m.src}:${m.target}:O"
            else
              "--mount type=${m.type},source=${m.src},target=${m.target}"
              + (if m.opts != "" then ",${m.opts}" else "")
          ) mounts;

          homeMountCmds = pkgs.lib.concatMapStringsSep "\n" (
            m: if pkgs.lib.hasPrefix "${HOME}/" m.target then "mkdir -p -m 700 .${m.target}" else ""
          ) mounts;

          policyConf = (pkgs.formats.json { }).generate "policy.json" {
            default = [ { type = "reject"; } ];
            transports = {
              docker-archive = {
                "" = [ { type = "insecureAcceptAnything"; } ];
              };
              oci-archive = {
                "" = [ { type = "insecureAcceptAnything"; } ];
              };
              dir = {
                "" = [ { type = "insecureAcceptAnything"; } ];
              };
            };
          };

          # Generate a Nix Database (sqlite) containing the registration info for all image contents.
          # We use the host's (build-time) Nix to generate the DB for the target paths.
          nixDb =
            pkgs.runCommand "nix-db"
              {
                buildInputs = [ pkgs.nix ];
              }
              ''
                mkdir -p $out/db
                export NIX_STATE_DIR=$out
                export NIX_STORE_DIR=${builtins.storeDir}
                # Load the registration info (hashes/validity) for the image contents into a fresh DB
                nix-store --load-db < ${pkgs.closureInfo { rootPaths = imageContents; }}/registration
              '';

          # Define contents list explicitly so we can use it for both the image and the DB generation
          imageContents =
            with pkgs;
            [
              bashInteractive
              coreutils
              curl
              fd
              git
              gnugrep
              less
              which
            ]
            ++ (pkgs.lib.optionals withNix [
              (pkgs.writeTextDir "etc/nix/nix.conf" ''
                experimental-features = nix-command flakes
                sandbox = false
                filter-syscalls = false
                trusted-users = root ${USER}
                build-users-group =
                use-cgroups = false
              '')
              nix
            ])
            ++ (pkgs.lib.optionals withPrivileges [
              podman
            ])
            ++ packages;

          agentImage = pkgs.dockerTools.buildLayeredImage {
            name = "agent-sandbox-image";
            tag = "latest";

            contents = imageContents;

            config = {
              User = USER;
              WorkingDir = "/workspace";
              Env = [
                "USER=${USER}"
                "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
              ]
              ++ (pkgs.lib.optionals withNix [
                "NIX_REMOTE=local"
                "NIX_SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
              ]);
              Cmd = [ "bash" ];
            };

            fakeRootCommands = ''
              mkdir -p -m 1777 tmp
              mkdir -p -m 777 workspace
              mkdir -p -m 755 .${HOME}
              chown 1000:1000 .${HOME}

              mkdir -p -m 700 etc root
              echo "root:x:0:0:root:/root:/bin/bash" > etc/passwd
              echo "${USER}:x:1000:1000:${USER}:/home/${USER}:/bin/bash" >> etc/passwd
              echo "root:x:0:" > etc/group
              echo "${USER}:x:1000:" >> etc/group

              # Create FHS compatibility symlinks (required for npm scripts using /usr/bin/env)
              mkdir -p usr/bin bin
              ln -s ${pkgs.coreutils}/bin/env usr/bin/env

            ''
            + (pkgs.lib.optionals withNix ''
              # Setup state needed for nix
              mkdir -p nix/var/nix
              cp -r ${nixDb}/db nix/var/nix/
              chmod -R 755 nix/var/nix
              chown -R 1000:1000 nix/var/nix
              mkdir -p nix/store
              chown -R 1000:1000 nix

            '')
            + homeMountCmds;
          };

          runContainer = pkgs.writeShellApplication {
            name = "run-container";
            runtimeInputs = [
              pkgs.podman
              pkgs.slirp4netns
            ];
            text = ''
              echo "${ICON} Agent Sandbox"
            ''
            + pkgs.lib.optionalString withNixVolume ''
              # Clean up any old nix-store overlays that don't match the current one
              # This ensures we don't use stale Nix store states
              echo "${ICON} Checking for outdated nix volume..."
              for vol in $(podman volume ls --format "{{.Name}}" | grep "^nix-store-overlay-"); do
                if [ "$vol" != "${nixVolume}" ]; then
                  echo "Removing outdated volume: $vol"
                  podman volume rm "$vol" >/dev/null 2>&1 || true
                fi
              done
            ''
            + ''
              cleanup() {
                echo "${ICON} Cleaning up image"
                podman image rm "agent-sandbox-image:latest" >/dev/null 2>&1 || true
              }
              trap cleanup EXIT INT TERM

              echo "${ICON} Loading image..." 
              podman load --quiet --signature-policy=${policyConf} --input "${agentImage}"

              # Check if we are already inside a container (Docker or Podman)
              # If we are, avoid using gVisor (runsc) because nested virtualization often fails or performs poorly.
              RUNTIME_FLAGS=("--runtime=${pkgs.gvisor}/bin/runsc" "--runtime-flag=ignore-cgroups")
              if [ -f "/.dockerenv" ] || [ -f "/run/.containerenv" ]; then
                echo "${ICON} Nested container detected. Using default runtime (runc)."
                RUNTIME_FLAGS=()
              fi

              echo "${ICON} Launching sandbox"
              podman run -it --rm \
                "''${RUNTIME_FLAGS[@]}" \
                --name agent-sandbox-instance \
                --cgroup-manager=cgroupfs \
                --events-backend=file \
                --network=slirp4netns \
                --userns=keep-id \
                --workdir /workspace \
            ''
            + pkgs.lib.optionalString (!withPrivileges) ''
                --security-opt=no-new-privileges \
                --cap-drop=all \
            ''
            + pkgs.lib.optionalString withPrivileges ''
                --device /dev/fuse \
            ''
            + ''
                ${mountFlags} \
                agent-sandbox-image:latest \
                bash
            '';
          };

          # VM Configuration (for Libvirt/QCOW2)
          vmImage = nixos-generators.nixosGenerate {
            inherit system;
            format = "qcow2";
            modules = [
              ({ config, lib, ... }: {
                 # Basic VM Settings
                 networking.hostName = "agent-sandbox";
                 networking.firewall.enable = false; # Allow traffic for dev
                 
                 # User Configuration
                 users.users.${USER} = {
                   isNormalUser = true;
                   extraGroups = [ "wheel" ];
                   initialPassword = "agent"; # Dev password
                 };
                 
                 # SSH Access
                 services.openssh.enable = true;
                 services.openssh.settings.PasswordAuthentication = true;
                 services.openssh.settings.PermitRootLogin = "yes";

                 # System Packages (Syncs with container)
                 environment.systemPackages = imageContents;
                 
                 # Optimization for QEMU
                 services.qemuGuest.enable = true;
              })
            ];
          };
        in
        {
          inherit agentImage runContainer vmImage;
        };

    in
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        sandbox = mkSandbox {
          inherit pkgs system;
          packages = with pkgs; [
            gh
            jj
            nodejs_22
            ripgrep
          ];
          # withPrivileges = true;
        };

        devcontainerConfig = (pkgs.formats.json { }).generate "devcontainer.json" {
          name = "Agent Sandbox";
          image = "agent-sandbox-image:latest";
          remoteUser = "agent";
          workspaceMount = "source=\${localWorkspaceFolder},target=/workspace,type=bind";
          workspaceFolder = "/workspace";
          customizations = {
            vscode = {
              settings = { };
              extensions = [ ];
            };
          };
        };

        # Helper script to export the image and configure VS Code Devcontainer
        devcontainer = pkgs.writeShellScriptBin "make-devcontainer" ''
          set -e
          IMAGE_PATH="${sandbox.agentImage}"
          
          echo "🔍 Finding container engine..."
          if command -v podman >/dev/null; then
            CMD="podman"
          elif command -v docker >/dev/null; then
            CMD="docker"
          else
            echo "❌ Error: Neither podman nor docker found in PATH."
            exit 1
          fi

          echo "📦 Loading image from $IMAGE_PATH..."
          $CMD load < "$IMAGE_PATH"

          echo "📝 Generating .devcontainer/devcontainer.json..."
          mkdir -p .devcontainer
          cp "${devcontainerConfig}" .devcontainer/devcontainer.json
          chmod 644 .devcontainer/devcontainer.json
          
          echo "✅ Done! Open the command palette in VS Code and select 'Dev Containers: Reopen in Container'."
        '';

      in
      {
        packages.image = sandbox.agentImage;
        packages.vm = sandbox.vmImage;
        packages.devcontainer = devcontainer;
        packages.container = sandbox.runContainer;
        packages.default = sandbox.runContainer;

        devShells.default = pkgs.mkShell {
          packages = [ sandbox.runContainer ];
          shellHook = ''
            exec run-container
          '';
        };
      }
    )
    // {
      lib.mkSandbox = mkSandbox;
    };
}
