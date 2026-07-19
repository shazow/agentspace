# `mkSandbox`

`agentspace.lib.mkSandbox { ... }` builds a NixOS configuration for an
Agentspace QEMU microVM. Pass the result to `agentspace.lib.mkLaunch` to get
a host-side launcher, or expose it as a `nixosConfiguration`.

```nix
let
  sandbox = agentspace.lib.mkSandbox {
    persistence.baseDir = "/home/me/vms/agentspace";
    machine.memory = 8 * 1024;
    ssh.authorizedKeys = [ "ssh-ed25519 AAAA..." ];
  };
in {
  nixosConfigurations.agentspace = sandbox;
  apps.x86_64-linux.default = {
    type = "app";
    program = agentspace.lib.mkLaunch sandbox;
  };
}
```

The options below are the public `agentspace.sandbox` API implemented by
[`sandbox-qemu.nix`](./sandbox-qemu.nix). Values shown are the defaults unless
noted otherwise.

## Guest identity and machine

| Option | Function |
| --- | --- |
| `user` (`"agent"`) | Guest user and home-directory owner. |
| `groups` (`[ "wheel" "kvm" ]`) | Extra groups for the guest user. |
| `hostName` (`"agent-sandbox"`) | Guest hostname and the generated manifest name. |
| `quiet` (`true`) | Suppress normal kernel/initrd output. Set `false` to print serial output while debugging boot failures. |
| `machine.memory` (`4096`) | Guest memory in MiB. |
| `machine.vcpu` (`null`) | vCPU count. `null` lets virtie choose the host-visible CPU count at launch time. |

The sandbox also enables QEMU, a user-mode network interface, the QEMU guest
agent, SSH, passwordless `sudo` for `wheel`, and a small base package set
(`bashInteractive`, `coreutils`, `curl`, `fd`, `git`, `grep`, `less`, `neovim`,
and `which`).

## SSH

| Option | Function |
| --- | --- |
| `ssh.authorizedKeys` (`[ ]`) | Public keys installed for the guest user. If keys are supplied, the generated launcher does not autoprovision SSH keys. |
| `ssh.command` (`""`) | Default command run in the guest when launching without arguments. |
| `ssh.exec` (`null`) | Complete host-side SSH argv. When unset, a suitable OpenSSH command is generated; `agentspace.lib.mkExecSSH` can build one with config/identity options. |
| `ssh.autoconnect` (`true`) | Automatically attach an SSH session when launching with no command and no explicit launcher arguments. |

Password and keyboard-interactive SSH authentication are disabled, and root
SSH login is disabled.

## Persistence and storage

| Option | Function |
| --- | --- |
| `persistence.baseDir` (`".agentspace"`) | Host directory prefix for generated images, state, and the virtie manifest. Relative paths are resolved from the launch directory. |
| `persistence.homeImage` (`"home.img"`) | Ext4 image for persistent `/home/<user>`. Set to `null` to disable the home image. Absolute paths are used as-is. |
| `persistence.homeSize` (`4096`) | Home image size in MB. |
| `persistence.storeOverlay` (`"nix-store-overlay-v2.img"`) | Writable Nix-store overlay image path. |
| `persistence.storeOverlaySize` (`8192`) | Size in MiB used when creating the writable Nix-store overlay image. Existing images are not resized. |
| `persistence.storeDisk` (`false`) | Use a generated read-only Nix-store disk instead of sharing the host `/nix/store`. |

`persistence.basedir` is a hidden, deprecated spelling of `baseDir`; new
configurations should use `persistence.baseDir`.

## Workspace and host shares

| Option | Function |
| --- | --- |
| `workspace.enable` (`true`) | Enable the workspace share and the guest `WORKSPACE` environment variable. |
| `workspace.guestDir` (`/home/<user>/workspace`) | Guest directory for the workspace. |
| `workspace.hostDir` (`${persistence.baseDir}/workspace`) | Host directory backing the default workspace share. It is created by the launcher. |
| `workspace.addCurrentDir` (`true`) | Mount the launch-time current directory at `/mnt/cwd` and make it available as a workspace. |
| `workspace.spaces` (`{ }`) | Additional named workspaces: `{ name = "/host/path"; }` mounts the path at `${workspace.guestDir}/name`. Nested names such as `project/src` are supported. |
| `shares` (`[ ]`) | Additional host directory shares using the `microvm.shares` schema. |

Disabling `workspace.enable` removes the default workspace mounts and
`WORKSPACE`. It also disables `swapSize` (an assertion rejects a non-zero swap
size without a workspace).

## Disks, networking, and runtime resources

| Option | Function |
| --- | --- |
| `volumes` (`[ ]`) | Additional disk images using the `microvm.volumes` schema. |
| `forwardPorts` (`[ ]`) | Additional user-network forwards using the `microvm.forwardPorts` schema. |
| `swapSize` (`0`) | Guest sparse swapfile size in MiB, created below the workspace. `0` disables it. |
| `balloon` (`false`) | Enable virtio-balloon and virtie's runtime balloon controller, including deflation on OOM and free-page reporting. |
| `nixStoreShareSocket` (`null`) | Existing host virtiofsd socket for the read-only Nix-store share. When set, virtie does not start its own daemon for that share; the path must be absolute and already be a socket when launching. |
| `virtiofsd.*` | Configure the virtiofsd package, group, thread-pool size, inode file-handle policy, and extra arguments using the `mkVirtioFSD` options. |

`agentspace.nixosModules.hostVirtiofsdNixStore` provides a ready-made socket
producer for `nixStoreShareSocket`: a socket-activated `virtiofsd` systemd
service on the **host** that shares `/nix/store` read-only at
`/run/virtiofs-nix-store.sock`. Import it into your host's NixOS
configuration (not the sandbox), enable it, and point the sandbox at the
resulting socket:

```nix
{
  imports = [ agentspace.nixosModules.hostVirtiofsdNixStore ];
  agentspace.hostVirtiofsdNixStore.enable = true;
}
```

```nix
agentspace.sandbox.nixStoreShareSocket = "/run/virtiofs-nix-store.sock";
```

| Option (`agentspace.hostVirtiofsdNixStore.*`) | Function |
| --- | --- |
| `enable` (`false`) | Enable the host-side socket-activated virtiofsd service. |
| `socketGroup` (`"kvm"`) | Group owning the virtiofsd socket. |
| `ownHardening` (`false`) | Disable virtiofsd's own sandboxing and rely on systemd hardening (`ProtectSystem`, `PrivateNetwork`, etc.) instead. |

The default writable store overlay is an 8 GiB image containing both the
OverlayFS upper layer and the native Nix state database. The guest still uses
the Nix daemon socket; only the daemon opens the `local-overlay` store directly.
This enables the experimental `local-overlay-store` and `read-only-local-store`
Nix features. Images created by the legacy writable-store overlay do not contain
the native database and are not supported. The launch wrapper warns when the
old default `nix-store-overlay.img` remains beside the new image.

The native backend does not make a mutable lower store safe. When the default
host `/nix/store` share is used, do not add, remove, or mutate host store paths
while the VM is running. Across restarts the lower store may grow, but removing
lower paths referenced by the persistent upper database is unsupported. In
particular, host garbage collection can still invalidate a retained sandbox.
`persistence.storeDisk = true` avoids concurrent host mutation by using an
immutable lower-store image.

Additional `volumes` are attached after the built-in store overlay and optional
home image. Additional
shares and forwards are merged with the built-in mounts and user-mode network.

## Host-side processes and notifications

### `run`

`run` is a list of host-side commands managed for the lifetime of the virtie
launch:

```nix
run = [
  {
    exec = [
      "xdg-dbus-proxy"
      "{{.Env.DBUS_SESSION_BUS_ADDRESS}}"
      "{{.Workspace.HostPath}}/dbus-proxy.sock"
      "--filter"
    ];
    vars.SocketDir = "/tmp/agentspace-sockets";
  }
];
```

`exec` is a non-empty argv. `vars` adds template variables. Templates can use
`Workspace.GuestPath`, `Workspace.HostPath`, `CID`, `StateDir`, entries in
`vars`, and `Env`. Each command also receives a `Config` variable containing
the effective user, workspace, and persistence settings.

### `notifications`

| Option | Function |
| --- | --- |
| `notifications.command` (`""`) | Host shell command run by virtie for runtime notifications. It can read `VIRTIE_NOTIFY_STATE` and `VIRTIE_NOTIFY_MESSAGE`. Empty disables the hook. |
| `notifications.states` (`[ ]`) | Optional state allowlist. Empty means all notification states. |

## Files injected into the guest

`writeFiles` is an attribute set keyed by absolute guest path. Each value may
contain:

| Field | Function |
| --- | --- |
| `text` (`null`) | Literal text to write. |
| `path` (`null`) | Host path whose bytes to write; useful for binary files. |
| `followLinks` (`true`) | Follow symlinks while reading `path`. |
| `writeBack` (`false`) | Copy the guest file back to the host path on shutdown or suspend. |
| `chown` (`null`) | Optional `user:group` ownership after writing. |
| `mode` (`null`) | Optional four-digit octal mode, such as `"0644"`. |
| `overwrite` (`false`) | Replace an existing guest file. |

Use either `text` or `path` for the file contents. The host path is read by
virtie at runtime rather than embedded as file contents in the Nix store.

Example:

```nix
writeFiles = {
  "/etc/my-agent/config.json" = {
    text = builtins.toJSON { enabled = true; };
    mode = "0644";
    overwrite = true;
  };
};
```

## Extending the guest

| Option | Function |
| --- | --- |
| `homeModules` (`[ ]`) | Home Manager modules imported for the primary guest user. |
| `extraModules` (`[ ]`) | Additional NixOS modules. Use this for packages, services, boot settings, or lower-level `microvm.*` options not represented above. |

For example:

```nix
extraModules = [
  ({ pkgs, ... }: {
    environment.systemPackages = [ pkgs.ripgrep ];
    microvm.graphics.enable = true;
  })
];

homeModules = [
  ({ ... }: {
    programs.git.enable = true;
  })
];
```

`extraModules` is the escape hatch for the underlying
[`microvm.nix`](https://github.com/astro/microvm.nix) option schemas, including
`microvm.shares`, `microvm.volumes`, `microvm.forwardPorts`, QEMU/kernel/
graphics settings, and other NixOS configuration. Prefer the dedicated
`shares`, `volumes`, and `forwardPorts` options when they are sufficient.

## Launch behavior

`mkSandbox` itself evaluates the VM; it does not start one. Use
`mkLaunch sandbox` to produce a launcher that:

1. creates the workspace host directory and persistence directory as needed;
2. writes the generated virtie TOML manifest;
3. reports the system closure size when available; and
4. runs virtie/QEMU, optionally attaching SSH or passing a default/explicit
   remote command.

The generated manifest also includes the built-in Nix-store share (unless
`persistence.storeDisk` is enabled), workspace mounts, optional images, port
forwards, host-side `run` processes, notifications, file injection, ballooning,
and SSH settings.

For a lower-level module integration, the same implementation is exported as
`agentspace.nixosModules.default`; in that case configure
`agentspace.sandbox.*` directly instead of calling `mkSandbox`.
