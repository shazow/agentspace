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

      mkBundle = inputs: pkgs.linkFarm "agent-bundle" (
        pkgs.lib.mapAttrsToList (name: path: { inherit name path; }) inputs
      );

      # Define the bundle: { "path/in/guest/home" = ./literal/path/on/host; }
      bundle = mkBundle {
        #".config/git/gitconfig" = /home/shazow/.config/git/gitconfig;
        # "./scripts" = ./scripts; # -> ~/workspace/scripts
      };
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
                    proto = "9p";
                    tag = "ro-store";
                    # a host's /nix/store will be picked up so that no squashfs/erofs will be built for it.
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
                    securityModel = "mapped";
                  }
                ] ++ pkgs.lib.optionals (bundle != {}) [
                  {
                    proto = "9p";
                    tag = "bundle";
                    source = "${bundle}";
                    mountPoint = "/mnt/bundle";
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

                # Ensure the home directory and workspace are owned by the user
                systemd.tmpfiles.rules = [
                  "d /home/${USER} 0700 ${USER} users -"
                  "d /home/${USER}/workspace 0755 ${USER} users -"
                  "f /home/${USER}/.bash_logout 0600 ${USER} users - sudo poweroff"
                ];

                systemd.services.unpack-bundle = lib.mkIf (bundle != {}) {
                  description = "Hydrate bundle files into home directory";
                  after = [ "local-fs.target" ]; 
                  before = [ "systemd-user-sessions.service" ];
                  wantedBy = [ "multi-user.target" ];
                  serviceConfig = {
                    Type = "oneshot";
                    User = USER;
                    WorkingDirectory = "/home/${USER}";
                  };
                  script = ''
                    if [ -d /mnt/bundle ]; then
                      cp -Lr /mnt/bundle/. .
                      chmod -R u+w .
                    fi
                  '';
                };

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
