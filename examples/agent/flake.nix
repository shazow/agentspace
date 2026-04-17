{
  description = "Example agent flake using agentspace.lib.mkSandbox";

  inputs = {
    agentspace.url = "path:../..";
    nixpkgs.follows = "agentspace/nixpkgs";
    microvm.follows = "agentspace/microvm";
  };

  outputs =
    {
      self,
      agentspace,
      nixpkgs,
      ...
    }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      defaultPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJeBQ2MawTu2l+nJvxR4hXsivy6a1fYu46yI5mJ8QHxr agent@agent-sandbox";
      validateAgent = pkgs.writeShellScriptBin "validate-agent" ''
        set -euo pipefail

        tmpdir=$(${pkgs.coreutils}/bin/mktemp -d)
        cleanup() {
          rm -rf "$tmpdir"
        }
        trap cleanup EXIT INT TERM

        key_path="$tmpdir/id_ed25519"
        host_name="agentspace-example-$$"
        vsock_cid=$((20000 + ($$ % 10000)))
        remote_command='set -eu; set -o pipefail; test -d "$HOME/workspace"; systemctl is-active --quiet sshd; awk '"'"'/MemTotal:/ { exit !($2 < 1572864) }'"'"' /proc/meminfo; echo AGENTSPACE_E2E_OK'
        printf -v remote_command_quoted '%q' "$remote_command"

        ${pkgs.openssh}/bin/ssh-keygen -q -t ed25519 -N "" -f "$key_path" >/dev/null
        public_key=$(${pkgs.coreutils}/bin/tr -d '\n' < "$key_path.pub")

        cat > "$tmpdir/flake.nix" <<EOF
        {
          description = "Temporary agentspace example validation";

          inputs.agentspace.url = "path:${../..}";
          inputs.example.url = "path:${./.}";
          inputs.example.inputs.agentspace.follows = "agentspace";
          inputs.example.inputs.nixpkgs.follows = "agentspace/nixpkgs";
          inputs.example.inputs.microvm.follows = "agentspace/microvm";

          outputs = { agentspace, example, ... }: {
            apps.${system}.default = {
              type = "app";
              program = agentspace.lib.mkLaunch (example.lib.mkExampleSandbox {
                publicKey = "$public_key";
                hostName = "$host_name";
                sshIdentityFile = "$key_path";
                storeOverlayPath = "$tmpdir/nix-store-overlay.img";
                vsockCid = $vsock_cid;
              });
            };
          };
        }
        EOF

        echo "🔎 Validating example flake with a 1024 MiB VM..."
        ${pkgs.coreutils}/bin/timeout --signal=TERM --kill-after=15s 300s \
          ${pkgs.nix}/bin/nix run --no-write-lock-file "$tmpdir" -- \
          "bash -lc $remote_command_quoted"
      '';
    in
    rec {
      lib.mkExampleSandbox =
        {
          publicKey ? defaultPublicKey,
          hostName ? "agentspace-example",
          homeImagePath ? null,
          protocol ? "9p",
          sshIdentityFile ? null,
          storeOverlayPath ? "/tmp/agentspace-example-store.img",
          vsockCid ? 11010,
        }:
        agentspace.lib.mkSandbox {
          connectWith = "ssh";
          hostName = hostName;
          protocol = protocol;
          sshAuthorizedKeys = [ publicKey ];
          inherit sshIdentityFile;
          persistence = {
            homeImage = homeImagePath;
            storeOverlay = storeOverlayPath;
          };
          extraModules = [
            {
              microvm.mem = 1024;
              microvm.vsock.cid = vsockCid;
            }
          ];
        };

      nixosConfigurations.agentspace = self.lib.mkExampleSandbox {
      };

      apps.${system}.default = {
        type = "app";
        program = "${validateAgent}/bin/validate-agent";
      };
    };
}
