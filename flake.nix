{
  description = "Agent Sandbox";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    let
      mkSandbox =
        {
          pkgs,
          additionalPkgs ? [ ],
          additionalMounts ? [ ],
        }:
        let
          USER = "agent";
          HOME = "/home/${USER}";

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
            # Persist Nix store and database.
            #{
            #  type = "volume";
            #  src = "${nixVolume}";
            #  target = "/nix";
            #  opts = "";
            #}
          ]
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

          nixConf = pkgs.writeTextDir "etc/nix/nix.conf" ''
            experimental-features = nix-command flakes
            sandbox = false
            filter-syscalls = false
            trusted-users = root ${USER}
            build-users-group =
            use-cgroups = false
          '';

          # Generate a Nix Database (sqlite) containing the registration info for all image contents.
          # We use the host's (build-time) Nix to generate the DB for the target paths.
          nixDb = pkgs.runCommand "nix-db" {
            buildInputs = [ pkgs.nix ];
          } ''
            mkdir -p $out/db
            export NIX_STATE_DIR=$out
            export NIX_STORE_DIR=${builtins.storeDir}
            # Load the registration info (hashes/validity) for the image contents into a fresh DB
            nix-store --load-db < ${pkgs.closureInfo { rootPaths = imageContents; }}/registration
          '';

          # Define contents list explicitly so we can use it for both the image and the DB generation
          imageContents = [
            nixConf
          ] ++ (with pkgs; [
            bashInteractive
            coreutils
            curl
            fd
            fish
            gh
            git
            gnugrep
            jj
            less
            nix
            nodejs_22
            ripgrep
            which
          ])
          ++ additionalPkgs;

          agentImage = pkgs.dockerTools.buildLayeredImage {
            name = "agent-sandbox-image";
            tag = "latest";

            contents = imageContents;

            config = {
              User = USER;
              WorkingDir = "/workspace";
              Env = [
                "USER=${USER}"
                "NIX_REMOTE=local"
                "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
                "NIX_SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
              ];
              Cmd = [ "fish" ];
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

              # Nix state
              mkdir -p nix/var/nix
              cp -r ${nixDb}/db nix/var/nix/
              chmod -R 755 nix/var/nix
              chown -R 1000:1000 nix/var/nix
              mkdir -p nix/store
              chown -R 1000:1000 nix

              ${homeMountCmds}
            '';
          };

          runScript = pkgs.writeShellApplication {
            name = "run-container";
            runtimeInputs = [
              pkgs.podman
              pkgs.slirp4netns
            ];
            text = ''
              IMAGE_ARCHIVE="${agentImage}"

              cleanup() {
                echo ""
                echo "--- Cleaning up Image ---"
                podman image rm "agent-sandbox-image:latest" >/dev/null 2>&1 || true
              }
              trap cleanup EXIT INT TERM

              echo "--- Loading Image from Nix Store ---"
              podman load --quiet --signature-policy=${policyConf} --input "$IMAGE_ARCHIVE"

              echo "--- Launching Sandbox (runsc) ---"
              ENV_FLAGS=""
              [ -n "''${GEMINI_API_KEY:-}" ] && ENV_FLAGS="$ENV_FLAGS --env GEMINI_API_KEY"
              [ -n "''${ANTHROPIC_API_KEY:-}" ] && ENV_FLAGS="$ENV_FLAGS --env ANTHROPIC_API_KEY"
              [ -n "''${OPENAI_API_KEY:-}" ] && ENV_FLAGS="$ENV_FLAGS --env OPENAI_API_KEY"

              # shellcheck disable=SC2086
              podman run -it --rm \
                --security-opt=no-new-privileges \
                --cap-drop=all \
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
        in
        {
          inherit agentImage runScript;
        };

    in
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        sandbox = mkSandbox { inherit pkgs; };
      in
      {
        packages.image = sandbox.agentImage;
        packages.default = sandbox.runScript;

        devShells.default = pkgs.mkShell {
          packages = [ sandbox.runScript ];
          shellHook = ''
            echo "Agent Sandbox Environment"
            exec run-container
          '';
        };
      }
    )
    // {
      lib.mkSandbox = mkSandbox;
    };
}
