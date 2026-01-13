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

      USER = "agent";
      HOSTNAME = "agent-sandbox";

      # TODO: Factor these out into arguments passed into a mkSandbox helper
      homeImagePath = "./home.img";
      withWorkspace = true;
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
                mem = 1024;
                balloon = true; # Allocate memory on demand
                shares = [
                  {
                    # use proto = "virtiofs" for MicroVMs that are started by systemd
                    proto = "9p";
                    tag = "ro-store";
                    # a host's /nix/store will be picked up so that no
                    # squashfs/erofs will be built for it.
                    source = "/nix/store";
                    mountPoint = "/nix/.ro-store";
                  }
                ] ++ pkgs.lib.optionals withWorkspace [
                  {
                    # Share for agent workspace
                    proto = "9p";
                    tag = "workspace";
                    source = ".";
                    mountPoint = "/home/${USER}/workspace";
                  }
                ];

                volumes = pkgs.lib.optionals ( homeImagePath != "" ) [
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

                writableStoreOverlay = "/nix/.rw-store";

                hypervisor = "qemu";
                qemu.extraArgs = [
                  "-cpu"
                  "host" # Allow nested emulation
                ];
                interfaces = [
                  {
                    type = "user";
                    id = "microvm1";
                    mac = "02:02:00:00:00:01";
                  }
                ];
              };

              fileSystems."/nix/.rw-store" = {
                fsType = "tmpfs";
                options = [ "mode=0755" ];
              };
            }
            (
              # configuration.nix
              { pkgs, lib, ... }:
              {
                networking.hostName = HOSTNAME;
                system.stateVersion = lib.trivial.release;
                nixpkgs.config.allowUnfree = true;
                nix.settings.experimental-features = [
                  "nix-command"
                  "flakes"
                ];

                boot.kernel.sysctl."kernel.unprivileged_userns_clone" = 1; # Nested namespaces
                # Quiet boot
                boot.kernelParams = [ "quiet" "udev.log_level=3" ];
                boot.consoleLogLevel = 0;
                boot.initrd.verbose = false;

                fileSystems."/" = {
                  fsType = "tmpfs";
                  options = [ "mode=0755" ];
                };

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
