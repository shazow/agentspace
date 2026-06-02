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
          agentspace.inputs.microvm.nixosModules.microvm-options
          agentspace.nixosModules.default
        ];
      };

      agentspaceOptionsJSON =
        let
          optionsJSON = nuscht-search.packages.${system}.mkOptionsJSON {
            modules = [ searchModule ];
            specialArgs = {
              inherit (agentspace.lib) mkExecSSH mkVirtioFSD;
            };
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
