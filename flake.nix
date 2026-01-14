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
      shareProto = "9p"; # "9p" runs in userland but is slower, "virtiofs" requires root but is fast

      USER = "agent";
      HOSTNAME = "agent-sandbox";

      # TODO: Factor these out into arguments passed into a mkSandbox helper
      homeImagePath = "./home.img";
      withWorkspace = true;

      # TODO: Implement bundling and unbundling during runtime
      bundle = [
        # "~/.config/git/gitconfig"
        # "~/.gemini/settings.json
      ];
    in
    {
      packages.${system} =
        let
          runner = self.nixosConfigurations.vm.config.microvm.declaredRunner;
        in {
          default = runner;
          vm = runner;
        };

      nixosConfigurations = {
        vm = nixpkgs.lib.nixosSystem {
          inherit system;
          modules = [
            microvm.nixosModules.microvm
            {
              microvm = {
                mem = 4 * 1024;
                balloon = true; # Allocate memory on demand
                shares = [
                  {
                    # use proto = "virtiofs" for MicroVMs that are started by systemd
                    proto = shareProto;
                    tag = "ro-store";
                    source = "/nix/store";
                    mountPoint = "/nix/.ro-store";
                  }
                ] ++ pkgs.lib.optionals withWorkspace [
                  {
                    # Share for agent workspace
                    proto = shareProto;
                    tag = "workspace";
                    source = ".";
                    mountPoint = "/home/${USER}/workspace";
                    securityModel = "mapped";
                  }
                ];

                writableStoreOverlay = "/nix/.rw-store";
                volumes = [
                  {
                    # TODO: Delete image after shutdown, since the nix db is not retained yet
                    # See https://microvm-nix.github.io/microvm.nix/shares.html#writable-nixstore-overlay
                    image = "nix-store-overlay.img";
                    mountPoint = "/nix/.rw-store";
                    size = 2048;
                  }
                ] ++ pkgs.lib.optionals ( homeImagePath != "" ) [
                  {
                    image = homeImagePath;
                    mountPoint = "/home/${USER}";
                    fsType = "ext4";
                    size = 1024; # MB
                    autoCreate = true;
                  }
                ];

                # Keep the socket away from the CWD to avoid mounting
                socket = "/tmp/vm-${HOSTNAME}.sock";
                hypervisor = "qemu";
                qemu.extraArgs = [
                  "-cpu" "host" # Allow nested emulation
                ];
                interfaces = [
                  {
                    type = "user";
                    id = "microvm1";
                    mac = "02:02:00:00:00:01";
                  }
                ];
              };
            }
            (
              # configuration.nix
              { pkgs, lib, ... }:
              {
                networking.hostName = HOSTNAME;
                system.stateVersion = lib.trivial.release;
                nixpkgs.config.allowUnfree = true;

                # Pin to host's nixpkgs
                nix.registry.nixpkgs.flake = nixpkgs;
                nix.nixPath = [ "nixpkgs=${nixpkgs}" ];
                nix.settings.experimental-features = [ "nix-command" "flakes" ];

                boot.kernel.sysctl."kernel.unprivileged_userns_clone" = 1; # Nested namespaces
                # Quiet boot
                boot.kernelParams = [ "quiet" "udev.log_level=3" ];
                boot.consoleLogLevel = 0;
                boot.initrd.verbose = false;

                # Ensure the home directory and workspace are owned by the user
                systemd.tmpfiles.rules = [
                  "d /home/${USER} 0700 ${USER} users -"
                  "d /home/${USER}/workspace 0755 ${USER} users -"
                  "f /home/${USER}/.bash_logout 0600 ${USER} users - sudo poweroff"
                ];

                # User
                users.users.${USER} = {
                  password = "";
                  isNormalUser = true;
                  extraGroups = [ "wheel" ]; # sudoer
                };

                security.sudo.wheelNeedsPassword = false;
                services.getty.autologinUser = USER;

                # Packages
                environment.systemPackages = with pkgs; [
                  bashInteractive
                  coreutils
                  curl
                  fd
                  git
                  gnugrep
                  less
                  neovim
                  which
                ];
              }
            )
          ];
        };
      };
    };
}
