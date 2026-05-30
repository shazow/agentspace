# agentspace

A full VM environment that is safe and can recursively run itself, great for agents to work on self-improving their environment.

**Status**: Prototyping

Most exploring concepts around ideas here: https://github.com/shazow/shazow.net/issues/87


## Architecture

- Agentspace is a nix flake that has two library outputs with high-level consumer APIs:
  - `lib.mkSandbox` produces a sandbox VM configuration with the help of `microvm.nix`, check [sandbox-qemu.nix](https://github.com/shazow/agentspace/blob/main/sandbox-qemu.nix) for all the options.
  - `lib.mkLaunch` takes the sandbox output and wires it up into an executable script that runs the VM with `virtie`.
- Virtie handles running a bunch if processes and wiring them up together, including: `virtiofsd` for volume mounts, `qemu` for the VM, notification hooks, socket sharing, memory ballooning, and other things.

Virtie is rapidly evolving, but the agentspace consumer interface should remain fairly stable.

## Usage

There's a couple of examples:
- Minimal example used for testing: [./examples/agent/](https://github.com/shazow/agentspace/tree/main/examples/agent)
- **Start here:** Practical example used day to day: [github:shazow/nixfiles?dir=vms/agentspace](https://github.com/shazow/nixfiles/tree/main/vms/agentspace) -- remove anything you don't need!

But to recap, here's a flake you can make to give it a try:

```nix
{
  inputs = {
    agentspace.url = "github:shazow/agentspace";
  };

  outputs =
    {
      agentspace,
      ...
    }:
    let
      sandbox = agentspace.lib.mkSandbox {
        # Where should the VM images and filesystem overlays live? Defaults to $PWD if empty, but that's messy
        persistence.baseDir = "/home/shazow/vms/agentspace";

        ssh.authorizedKeys = [
          # Put your SSH public keys in here, one string per list item
          #"ssh-ed25519 AAAAC3N..."
        ];

        # VM settings
        #machine.vcpu = 8; # Default: Use all available cores (recommended unless you want a slow VM)
        machine.memory = 8 * 1024;

        # Additional mounts and port forwards can be configured directly:
        #shares = [];
        #volumes = [];
        #forwardPorts = [];

        # NixOS modules
        #extraModules = [];

        # home-manager modules for the primary user:
        #homeModules = [];

        # Optional host-side processes managed with the VM.
        #run = [
        #  {
        #    vars.SocketDir = "/tmp/agentspace-sockets";
        #    exec = [ "xdg-dbus-proxy" "{{.Env.DBUS_SESSION_BUS_ADDRESS}}" "{{.SocketDir}}/dbus-notifications.sock" "--filter" ];
        #  }
        #];
      };
    in
    {
      nixosConfigurations.agentspace = sandbox;

      apps."x86_64-linux" = {
        default = {
          type = "app";
          program = agentspace.lib.mkLaunch sandbox;
        };
      };
    };
}
```

## License

MIT
