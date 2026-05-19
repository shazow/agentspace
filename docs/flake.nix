{
  description = "Agentspace option search documentation";

  inputs.agentspace.url = "path:..";

  inputs.nuscht-search = {
    url = "github:NuschtOS/search";
    inputs.nixpkgs.follows = "agentspace/nixpkgs";
  };

  outputs =
    {
      agentspace,
      nuscht-search,
      ...
    }:
    let
      system = "x86_64-linux";
      pkgs = agentspace.inputs.nixpkgs.legacyPackages.${system};

      searchModule = {
        imports = [
          agentspace.nixosModules.default
          (
            { lib, ... }:
            {
              # Documentation-only shim for the upstream microvm share schema
              # reused by agentspace.sandbox.shares.
              options.microvm.shares = lib.mkOption {
                type = lib.types.listOf (lib.types.submodule {
                  options = {
                    proto = lib.mkOption {
                      type = lib.types.enum [
                        "virtiofs"
                        "9p"
                      ];
                      default = "virtiofs";
                      description = "Share transport used for this mount.";
                    };
                    tag = lib.mkOption {
                      type = lib.types.str;
                      description = "QEMU mount tag used to identify the share.";
                    };
                    source = lib.mkOption {
                      type = lib.types.str;
                      description = "Host path exported into the sandbox.";
                    };
                    mountPoint = lib.mkOption {
                      type = lib.types.str;
                      description = "Guest path where the share is mounted.";
                    };
                    securityModel = lib.mkOption {
                      type = lib.types.enum [
                        "none"
                        "passthrough"
                        "mapped"
                        "mapped-xattr"
                      ];
                      default = "none";
                      description = "QEMU filesystem security model for the share.";
                    };
                    readOnly = lib.mkOption {
                      type = lib.types.bool;
                      default = false;
                      description = "Whether the guest sees the share as read-only.";
                    };
                    cache = lib.mkOption {
                      type = lib.types.enum [
                        "auto"
                        "always"
                        "never"
                      ];
                      default = "auto";
                      description = "virtiofsd cache mode for the share.";
                    };
                    socket = lib.mkOption {
                      type = lib.types.str;
                      default = "";
                      description = "Host-side virtiofsd socket path for virtiofs shares.";
                    };
                  };
                });
                default = [ ];
                internal = true;
              };
            }
          )
        ];
      };

      agentspaceOptionsJSON =
        let
          optionsJSON = nuscht-search.packages.${system}.mkOptionsJSON {
            modules = [ searchModule ];
            specialArgs = { };
          };
        in
        pkgs.runCommand "agentspace-options.json"
          {
            nativeBuildInputs = [ pkgs.jq ];
          }
          ''
            jq 'with_entries(select(.key | startswith("agentspace.sandbox")))' \
              ${optionsJSON} > $out
          '';
    in
    {
      packages.${system}.default = nuscht-search.packages.${system}.mkSearch {
        title = "Agentspace Options";
        baseHref = "/agentspace/";
        optionsJSON = agentspaceOptionsJSON;
        urlPrefix = "https://github.com/shazow/agentspace/blob/main/";
      };
    };
}
