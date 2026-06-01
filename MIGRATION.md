# Migration Guide

This file tracks consumer-facing API changes and the steps needed to migrate
existing usage. Add a new dated section whenever a public command, Nix option,
flake output, manifest contract, or generated wrapper behavior changes.

## 2026-06-01: managed virtiofsd defaults avoid unprivileged warnings

### Who Is Affected

- Users launching agentspace as a normal user with managed virtiofs mounts.

### What Changed

The Nix-generated `virtiofsd` wrapper no longer uses the hardcoded nofile limit
or file handle behavior that produced warnings for normal users. It keeps
`virtiofsd`'s namespace sandbox, maps namespace root to the launching uid/gid
for non-root launches, sets nofile to the inherited hard limit, and uses
`--inode-file-handles=prefer` only when `CAP_DAC_READ_SEARCH` is effective.

### Migration Steps

No manifest change is required for Nix-generated managed virtiofs mounts.

## 2026-06-01: virtie hotplug entries moved under source device groups

### Who Is Affected

- Direct virtie manifest producers that emit hotplug entries.
- Producers using `mounts[].hotplugged = true`.

### What Changed

Hotplug devices now use the same input shape as their launch-time device
groups. To hotplug a mount, move it from `[[mounts]]` to
`[[hotplug.mounts]]`. To hotplug a network, move it from `[[networks]]` to
`[[hotplug.networks]]`.

The `hotplugged = true` mount flag and the typed `[[hotplug]]` list are no
longer accepted. Image mount formats are declared as `image.format`, not a
top-level `format` field.

### Migration Steps

Change hotplugged virtiofs mounts from:

```toml
[[mounts]]
type = "virtiofs"
tag = "cache"
source = "/tmp/cache"
hotplugged = true
target = "/mnt/cache"
virtiofs.socket = "cache.sock"
```

to:

```toml
[[hotplug.mounts]]
type = "virtiofs"
tag = "cache"
source = "/tmp/cache"
target = "/mnt/cache"
virtiofs.socket = "cache.sock"
```

Change network hotplug entries from:

```toml
[[hotplug]]
type = "net"
id = "vpn"
backend = "user"
mac = "02:02:00:00:00:10"
```

to:

```toml
[[hotplug.networks]]
id = "vpn"
type = "user"
mac = "02:02:00:00:00:10"
```

For hotplugged image mounts, use:

```toml
[[hotplug.mounts]]
type = "image"
source = "data.qcow2"
image.serial = "data"
image.format = "qcow2"
```

## 2026-05-30: ordered tagged virtie mounts restored

### Who Is Affected

- Direct virtie manifest producers using the grouped `[[mounts.virtiofs]]`,
  `[[mounts.9p]]`, or `[[mounts.image]]` sections.
- Consumers reading generated manifests and expecting `mounts` to be a table of
  backend-specific arrays.

### What Changed

The virtie manifest uses an ordered `[[mounts]]` array again. Each entry carries
`type = "virtiofs"`, `type = "9p"`, or `type = "image"` while backend-specific
settings remain nested under the backend name. The grouped mount format is no
longer accepted.

### Migration Steps

Move each grouped mount entry into `[[mounts]]` and add its backend type:

```toml
-[[mounts.virtiofs]]
+[[mounts]]
+type = "virtiofs"
 tag = "workspace"
 source = "."
 read_only = false
 virtiofs.socket = "workspace.sock"
```

```toml
-[[mounts.9p]]
+[[mounts]]
+type = "9p"
 tag = "cache"
 source = ".cache"
 9p.security_model = "mapped"
```

```toml
-[[mounts.image]]
+[[mounts]]
+type = "image"
 source = ".virtie/root.img"
 image.size = 4096
 image.fs = "ext4"
 image.create = true
```

## 2026-05-29: virtie image disks moved under mounts

### Who Is Affected

- Direct virtie manifest producers using top-level `[[volumes]]`.
- Consumers reading generated manifests and expecting a top-level `volumes`
  array.

### What Changed

The virtie manifest no longer accepts top-level `[[volumes]]`. Disk images are
now a mount backend under `[[mounts.image]]`. The image path uses the common
mount field `source`, read-only state remains common, and image-specific
settings are nested under `image`.

Nix `mkSandbox` users can now set `agentspace.sandbox.volumes` and
`agentspace.sandbox.forwardPorts` directly. Existing lower-level
`microvm.volumes`, `microvm.shares`, and `microvm.forwardPorts` definitions are
still merged into the evaluated sandbox.

### Migration Steps

Move each `[[volumes]]` entry to `[[mounts.image]]`:

```toml
-[[volumes]]
-image = ".virtie/root.img"
-read_only = false
-size = 4096
-fs = "ext4"
-create = true
-label = "root"
-direct = false
+[[mounts.image]]
+source = ".virtie/root.img"
+read_only = false
+
+[mounts.image.image]
+size = 4096
+fs = "ext4"
+create = true
+label = "root"
+direct = false
```

## 2026-05-29: typed virtie mount manifest sections

### Who Is Affected

- Direct virtie manifest producers setting `mounts` as a list of entries with
  `type = "virtiofs"` or `type = "9p"`.

### What Changed

The virtie manifest no longer uses polymorphic mount entries. Mounts are now
grouped by backend under `mounts.virtiofs` and `mounts.9p`. Common fields
stay on each mount entry, while backend-specific fields stay nested under the
backend name.

### Migration Steps

Move old `[[mounts]]` entries to the backend-specific array:

```toml
-[[mounts]]
-type = "virtiofs"
+[[mounts.virtiofs]]
 tag = "workspace"
 source = "."
 read_only = false
 virtiofs.socket = "workspace.sock"
 virtiofs.bin = "virtiofsd"
 virtiofs.args = ["--socket-path={{.Socket}}", "--shared-dir={{.MountSource}}", "--tag={{.MountTag}}"]
```

Move 9p entries to `[[mounts.9p]]` and nest 9p-specific options:

```toml
-[[mounts]]
-type = "9p"
+[[mounts.9p]]
 tag = "cache"
 source = ".cache"
 read_only = true
-security_model = "mapped"
+9p.security_model = "mapped"
```

## 2026-05-27: typed virtie manifest hotplug entries

### Who Is Affected

- Direct virtie manifest producers that want runtime device hotplug.
- Direct manifest producers validating allowed manifest keys.

### What Changed

The manifest now accepts ordered typed hotplug lists using `[[hotplug]]` with
`type = "virtiofs"`, `type = "net"`, or `type = "image"`. The command is
`virtie hotplug --manifest=MANIFEST ID`, with `--detach` for removal. QEMU
starts with one preallocated PCIe root port per typed hotplug entry.

The earlier arbitrary-QMP shape is removed before release. Manifest producers
should not emit `[[hotplug]]` entries with `attach.qmp`, `detach.qmp`,
`exec_guest`, `vars`, or host `exec` fields.

The earlier `qemu.allocate_pcie_ports` knob is also removed before release.
Manifest producers should declare typed hotplug entries up front instead of
reserving spare ports.

Virtiofs mounts can opt into the shortcut with `hotplugged = true`. These mounts
are not attached during launch; they generate a hotplug entry instead. Set
`target = "/guest/path"` when `virtie hotplug` should run `mount -t virtiofs`
inside the guest. If `target` is omitted, attach only adds the QEMU device and
the guest must mount it through fstab, udev, or a manual command.

### Migration Steps

No existing launch manifests need to change. To add a hotplugged virtiofs share:

```toml
[[mounts]]
type = "virtiofs"
tag = "cache"
source = "/tmp/cache"
hotplugged = true
target = "/mnt/cache"
virtiofs.socket = "cache.sock"
```

Then attach or detach it while the VM is running:

```console
virtie hotplug --manifest=manifest.toml cache
virtie hotplug --manifest=manifest.toml --detach cache
```

To define the same share explicitly:

```toml
[[hotplug]]
type = "virtiofs"
id = "{{.Tag}}"
tag = "cache"
source = "/tmp/cache"
target = "/mnt/cache"
virtiofs.socket = "cache.sock"
```

Net and block hotplug are intentionally minimal in this version:

```toml
[[hotplug]]
type = "net"
id = "vpn"
backend = "user"
mac = "02:02:00:00:00:10"

[[hotplug]]
type = "image"
id = "{{.Serial}}"
source = "data.qcow2"
serial = "data"
format = "qcow2"
```

These attach and detach QEMU devices only. Guest-side network setup, block
discovery, filesystem policy, and mounting are future work.

## 2026-05-22: generic run commands replace runWithTunnel

### Who Is Affected

- Consumers setting `agentspace.sandbox.runWithTunnel`.
- Direct manifest producers setting `run_with_tunnel`.
- Consumers setting `agentspace.sandbox.workspace.baseDir`.
- Direct manifest producers setting `workspace.basedir`.
- Direct manifest producers setting `mounts[].virtiofsd_socket` or
  `mounts[].virtiofsd_exec`.

### What Changed

`runWithTunnel` and manifest `run_with_tunnel` were removed. Use
`agentspace.sandbox.run` or manifest `[[run]]` for host-side processes managed
for the VM lifetime. `run` starts before QEMU and is stopped with the VM, but it
does not wait for socket readiness.

The automatic tunnel directories were removed. `virtie` no longer creates
`<persistence.baseDir>/tunnels`, no longer mounts it into the guest at
`/run/tunnels`, and no longer provides `Socket` or `GuestSocket` tunnel
template values. `SocketDir` is not a built-in; set it explicitly in `vars`
when needed.

Workspace paths were split:

- `agentspace.sandbox.workspace.baseDir` -> `workspace.guestDir`
- new `agentspace.sandbox.workspace.hostDir`
- manifest `workspace.basedir` -> `workspace.guest_dir`
- new manifest `workspace.host_dir`

In `run[].exec`, `{{.Workspace.GuestPath}}` is the guest workspace path and
`{{.Workspace.HostPath}}` is the host workspace path. The former scalar
`{{.Workspace}}` value was removed. Generated `{{.Config.workspace.hostDir}}`
remains available for compatibility, but new templates should use
`{{.Workspace.HostPath}}`.

Virtiofs mount daemon fields are now nested:

- `mounts[].virtiofsd_socket` -> `mounts[].virtiofs.socket`
- `mounts[].virtiofsd_exec` -> `mounts[].virtiofs.bin` and
  `mounts[].virtiofs.args`

When `virtiofs.socket` points at an existing socket, `virtie` uses it as
externally managed. Otherwise it starts `virtiofs.bin` with `virtiofs.args`.

### Migration Steps

Move host-side socket or proxy commands into `run` and choose explicit host
output paths:

```nix
agentspace.sandbox.run = [
  {
    vars.Name = "notifications";
    vars.SocketDir = "/tmp/agentspace-sockets";
    exec = [
      "xdg-dbus-proxy"
      "{{.Env.DBUS_SESSION_BUS_ADDRESS}}"
      "{{.SocketDir}}/dbus-notifications.sock"
      "--filter"
      "--name={{.Name}}"
    ];
  }
];
```

If the guest needs access to a produced socket, place it under an existing host
share such as `workspace.hostDir` and refer to the corresponding guest path
through `workspace.guestDir`.

## 2026-05-22: swapfile moved under WORKSPACE

### Who Is Affected

- Consumers setting `agentspace.sandbox.swapSize`.
- Consumers setting `agentspace.sandbox.swapSize` while disabling
  `agentspace.sandbox.workspace.enable`.

### What Changed

When `swapSize > 0`, the generated swapfile is now
`<workspace.baseDir>/swapfile` instead of `/swapfile`. This keeps large swap
files out of the root overlay and places them on the host-backed WORKSPACE
mount.

`swapSize > 0` now requires `agentspace.sandbox.workspace.enable = true`.

### Migration Steps

If you use `swapSize`, keep workspace mounts enabled. The default
configuration already does this.

## 2026-05-22: sandbox baseDir option spelling

### Who Is Affected

- Consumers setting `agentspace.sandbox.persistence.basedir`.
- Consumers setting `agentspace.sandbox.workspace.basedir`.

### What Changed

The Nix module options now use camelCase:

- `agentspace.sandbox.persistence.baseDir`
- `agentspace.sandbox.workspace.baseDir`

The old `basedir` spellings now fail evaluation with an assertion that names
the replacement option.

### Migration Steps

```diff
- agentspace.sandbox.persistence.basedir = ".agentspace";
+ agentspace.sandbox.persistence.baseDir = ".agentspace";

  agentspace.sandbox.workspace = {
-   basedir = "/home/agent/workspace";
+   baseDir = "/home/agent/workspace";
  };
```

## 2026-05-21: Manifest exec entries use Go templates

### Who Is Affected

- Direct virtie manifest producers setting `qemu.fwd_tunnel_exec`.
- Consumers using shell wrappers in manifest exec arrays.

### What Changed

Manifest exec arrays now render Go `text/template` variables before launch.
For native argv entries, use template variables such as `{{.Host}}` and
`{{.Port}}`. For commands that `virtie` starts directly, the same values are
also exposed to shell commands as environment variables.

`qemu.fwd_tunnel_exec` is an exception: QEMU starts each `guestfwd cmd:`
itself, so `HOST` and `PORT` are not injected into the environment. Use
template variables there, including inside shell command strings.

| Surface | Template values | Injected environment |
| --- | --- | --- |
| `qemu.exec` | `HostName`, `WorkingDir`, `StateDir`, `HostOS`, `HostArch`, `HostSystem`, `.Env` | none |
| `qemu.fwd_tunnel_exec` | `Host`, `Port`, `.Env` | none; QEMU starts the command |
| `ssh.exec` | `CID`, `User`, `Destination`, `.Env` | `CID`, `USER`, `DESTINATION` |
| `mounts[].virtiofsd_exec` | `Socket`, `Tag`, `.Env` | `SOCKET`, `TAG` |
| `notifications.exec` | `State`, `Message`, notification context values, `.Env` | `STATE`, `MESSAGE`, normalized context values |

### Migration Steps

Update direct `qemu.fwd_tunnel_exec` manifests that relied on literal `$HOST`
and `$PORT` in non-shell argv entries:

```toml
- fwd_tunnel_exec = ["nc", "$HOST", "$PORT"]
+ fwd_tunnel_exec = ["nc", "{{.Host}}", "{{.Port}}"]
```

Shell forms should use templates inside the shell string:

```toml
fwd_tunnel_exec = ["sh", "-c", "socat - TCP:{{.Host}}:{{.Port}}"]
```

## 2026-05-20: Workspace options replace legacy current-directory mount

### Who Is Affected

- Consumers setting `agentspace.sandbox.mountWorkspace`.
- Consumers setting `agentspace.sandbox.workspaceMountPoint`.
- Consumers relying on the default current directory mount at
  `/home/<user>/workspace`.

### What Changed

`agentspace.sandbox.mountWorkspace` and
`agentspace.sandbox.workspaceMountPoint` were removed. Use
`agentspace.sandbox.workspace` instead.

Current-directory mounting is enabled by default through
`agentspace.sandbox.workspace.addCurrentDir`. The launch working directory is
mounted in the guest under `workspace.baseDir` using the basename of the
resolved working directory. Set `agentspace.sandbox.workspace.enable = false`
to disable workspace mounts entirely, or set
`agentspace.sandbox.workspace.addCurrentDir = false` to keep fixed
`workspace.spaces` mounts without mounting the launch directory.

### Migration Steps

Replace legacy current-directory options:

```diff
- mountWorkspace = true;
- workspaceMountPoint = "/home/agent/workspace";
+ workspace = {
+   enable = true;
+   addCurrentDir = true;
+   baseDir = "/home/agent/workspace";
+ };
```

For fixed workspace mounts, use `workspace.spaces`:

```nix
workspace = {
  enable = true;
  spaces = {
    agentspace = "/home/example/projects/agentspace";
  };
};
```

## 2026-05-20: mkExecSSH helper replaces SSH path options

### Who Is Affected

- Consumers setting `agentspace.sandbox.ssh.identityFile` or
  `agentspace.sandbox.ssh.configFile`.
- Consumers that want the common OpenSSH command built for them.

### What Changed

`agentspace.sandbox.ssh.identityFile` and `agentspace.sandbox.ssh.configFile`
were removed. Use `agentspace.lib.mkExecSSH` to build `ssh.exec` instead.

### Migration Steps

Replace option fragments with the helper:

```nix
ssh.exec = agentspace.lib.mkExecSSH {
  identityFile = "./id_ed25519";
  configFile = "ssh_config";
};
```

If you were only using the old options for the generated default launcher,
just drop them and keep `ssh.authorizedKeys` / `ssh.exec` as needed.

## 2026-05-20: mkSandbox manifest output switched to TOML

### Who Is Affected

- Consumers that inspect `agentspace.sandbox.launch.virtieManifest` or
  `virtieManifestTemplate` paths.
- Consumers that assume the generated manifest file is JSON.

### What Changed

`mkSandbox` now writes the launch manifest as TOML instead of JSON. The
runtime manifest path now ends in `.toml`, and the copied template is a TOML
file generated from the same manifest data.

### Migration Steps

Update any path assertions, file globs, or tooling that expects
`virtie-<host>.json` to use `virtie-<host>.toml` instead.

## 2026-05-25: `kernel.serial_console` replaced by `kernel.serial`

### Who Is Affected

- Consumers writing `virtie` manifests directly.
- Tooling that generates or validates `virtie` manifests.

### What Changed

The `kernel.serial_console` boolean manifest field was removed. Use
`kernel.serial` instead:

```toml
[kernel]
serial = "off"      # no serial output
serial = "print"    # stream serial output without an interactive guest prompt
serial = "console"  # full interactive serial console configuration
```

Generated agentspace sandboxes default to `serial = "off"` when
`agentspace.sandbox.quiet = true` and `serial = "print"` when
`agentspace.sandbox.quiet = false`.

### Migration Steps

```diff
- serial_console = true
+ serial = "console"
```

For boot logs without an interactive serial prompt, use:

```toml
serial = "print"
```

## 2026-05-25: `agentspace.sandbox.serial` removed

### Who Is Affected

- Consumers setting `agentspace.sandbox.serial` in `mkSandbox`.

### What Changed

The Nix sandbox layer no longer exposes a serial mode override. Generated
manifests derive `kernel.serial` from `agentspace.sandbox.quiet`: quiet
sandboxes emit `serial = "off"`, and `quiet = false` emits `serial = "print"`.

### Migration Steps

Remove `agentspace.sandbox.serial`. To choose a serial mode explicitly, set
`kernel.serial` in the `virtie` manifest instead.

## 2026-05-18: Default launch SSH is no longer quiet

### Who Is Affected

- Consumers that inspect generated manifest `ssh.exec`.
- Consumers that expect the default SSH command to include `-q`.

### What Changed

The generated default `ssh.exec` no longer includes `-q`. This lets `virtie`
see SSH authentication failures and trigger default key autoprovisioning.

Custom `agentspace.sandbox.ssh.exec` values are still used as-is.

### Migration Steps

No change is needed for normal launches. If you provide a custom SSH command and
also rely on autoprovisioning, avoid suppressing authentication diagnostics.

## 2026-05-18: writeFiles write-back

### Who Is Affected

- Consumers that want files injected with `writeFiles` to persist guest-side
  changes back to the host.
- Direct manifest producers using `write_files[].source`.

### What Changed

`writeFiles` entries with a host source path can now opt into guest-to-host
write-back. In Nix, set `agentspace.sandbox.writeFiles.*.writeBack = true`.
In direct manifests, set `write_files[].write_back = true`.

The default is `false`, so existing `writeFiles` entries remain one-way
host-to-guest writes. `writeBack` requires a host source path because inline
`text` entries do not have a host destination.

### Migration Steps

No change is needed unless you want guest changes to persist back to the host:

```nix
agentspace.sandbox.writeFiles."/etc/example.conf" = {
  path = "./example.conf";
  writeBack = true;
};
```

## 2026-05-18: writeFiles source symlink control

### Who Is Affected

- Consumers setting `agentspace.sandbox.writeFiles.*.path` to a symlink.
- Direct manifest producers setting `write_files[].source` to a symlink.

### What Changed

Host source file reads for `writeFiles` now expose explicit symlink behavior.
`agentspace.sandbox.writeFiles.*.followLinks` lowers to
`write_files[].follow_links` in direct manifests. The default is `true`, which
preserves the previous behavior of reading through symlinks.

Set `followLinks = false` in Nix or `follow_links = false` in direct manifests
to reject symlink source paths before copying bytes into the guest.

### Migration Steps

No change is needed unless you want symlink source paths to fail closed:

```nix
agentspace.sandbox.writeFiles."/etc/example.conf" = {
  path = "./example.conf";
  followLinks = false;
};
```

## 2026-05-18: Launch wrapper closure size output

### Who Is Affected

- Consumers that parse generated launch wrapper stdout or stderr exactly.

### What Changed

The generated `mkLaunch` wrapper now prints a best-effort
`mkSandbox closure size` line before starting `virtie`. If the closure size
query is unavailable, the wrapper prints a warning to stderr and continues.

### Migration Steps

No change is needed for interactive users. Automation that expects exact wrapper
output should tolerate the new closure size line.

## 2026-05-18: External virtiofs socket preflight

### Who Is Affected

- Consumers setting `agentspace.sandbox.nixStoreShareSocket`.
- Direct virtie manifest producers using virtiofs mounts with
  `virtiofsd_socket` but no managed `virtiofsd_exec`.

### What Changed

`agentspace.sandbox.nixStoreShareSocket` must now be an absolute socket path
when set, and the generated launch wrapper checks that the path exists as a Unix
socket before starting `virtie`.

`virtie` also preflights external virtiofs sockets and fails before QEMU starts
when a socket path is missing or is not a socket.

### Migration Steps

Ensure external virtiofs socket producers are started before launching the
sandbox, and pass an absolute socket path:

```diff
- agentspace.sandbox.nixStoreShareSocket = "virtiofs-nix-store.sock";
+ agentspace.sandbox.nixStoreShareSocket = "/var/run/virtiofs-nix-store.sock";
```

## 2026-05-18: Explicit default SSH vsock proxy

### Who Is Affected

- Consumers that inspect generated manifest `ssh.exec`.
- Consumers relying on host SSH config to supply vsock proxy settings.

### What Changed

The default generated `ssh.exec` now includes an explicit
`systemd-ssh-proxy` `ProxyCommand`, fd-pass support, and ephemeral-host
checking options for vsock destinations. The default no longer depends on
matching entries in the user's SSH config.

Consumers that set `agentspace.sandbox.ssh.exec` keep full control and do not
receive these default proxy arguments automatically.

### Migration Steps

No change is needed for normal `mkSandbox` users. Consumers that assert the
exact generated `ssh.exec` list should update their expectations to include the
new `-o ProxyCommand=...`, `-o ProxyUseFdpass=yes`, and `-o CheckHostIP=no`
arguments.

## 2026-05-18: Configurable sandbox SSH exec

### Who Is Affected

- Consumers that need to replace the generated host-side SSH command.

### What Changed

`agentspace.sandbox.ssh.exec` now accepts a complete host-side SSH argv override.
When unset, agentspace keeps generating the default OpenSSH command and appends
`agentspace.sandbox.ssh.identityFile` when configured.

When `ssh.exec` is set, the override is used as-is; `ssh.identityFile` is not
appended automatically.

### Migration Steps

No change is needed for existing consumers. To override SSH execution, set the
complete argv:

```nix
agentspace.sandbox.ssh.exec = [
  "/usr/bin/ssh"
  "-F"
  "ssh_config"
];
```

## 2026-05-17: SSH readiness token and socket

### Who Is Affected

- Consumers that inspect generated virtie manifests or QEMU arguments.
- Custom guests that write the SSH readiness signal themselves.

### What Changed

Generated manifests still use the existing `ssh.ready_socket` field, but the
default generated value is now `ready.sock` instead of `ssh-ready.sock`. The
guest signal service now writes `SSH-READY` to the generic `virtie.ready`
virtio-serial port instead of writing `READY` to `virtie.ssh.ready`.

### Migration Steps

Update custom guest readiness writers and socket assertions:

```diff
- echo READY > /dev/virtio-ports/virtie.ssh.ready
+ echo SSH-READY > /dev/virtio-ports/virtie.ready
```

## 2026-05-17: SSH autoprovisioning

### Who Is Affected

- Direct virtie manifest producers that want default SSH key provisioning.
- Consumers reading default generated manifest `ssh.exec` or `write_files`.

### What Changed

Generated manifests now emit `ssh.autoprovision = true` when no explicit
identity file or authorized keys are configured. `virtie` generates the default
key in the state directory only after SSH reaches public-key authentication and
fails, then appends the public key to the guest user's `authorized_keys`.

Generated manifests no longer add an implicit `.agentspace/id_ed25519` to
`ssh.exec`, and no longer add an implicit `write_files` entry for
`authorized_keys`.

### Migration Steps

Direct manifest producers can opt in explicitly:

```toml
[ssh]
autoprovision = true
```

No change is needed when setting explicit `ssh.exec` identity arguments or
managing `authorized_keys` directly.

## 2026-05-17: guest forward tunnel command

### Who Is Affected

- Direct virtie manifest producers setting `host.netcat`.
- Consumers reading `agentspace.sandbox.launch.virtieManifestData.host.netcat`.

### What Changed

Guest-to-host `networks[].forward` entries now use `qemu.fwd_tunnel_exec`, an
argv template expanded into QEMU's `guestfwd ... cmd:` command. The `$HOST` and
`$PORT` variables are replaced from the forward rule's host endpoint.

Generated manifests no longer emit `host.netcat`; they emit a pinned netcat
template under `qemu.fwd_tunnel_exec`.

### Migration Steps

Move the command under `qemu` and include endpoint template variables:

```diff
- [host]
- netcat = "nc"
+ [qemu]
+ fwd_tunnel_exec = ["nc", "{{.Host}}", "{{.Port}}"]
```

For socat:

```toml
[qemu]
fwd_tunnel_exec = ["socat", "-", "TCP:{{.Host}}:{{.Port}}"]
```

## 2026-05-17: SSH retry delay seconds

### Who Is Affected

- Direct virtie manifest producers setting `ssh.retry_delay_ms`.
- Consumers reading lowered/internal manifests that previously used
  `ssh.retryDelayMs`.

### What Changed

The public manifest field `ssh.retry_delay_ms` was renamed to
`ssh.retry_delay`, and the value is now a floating-point delay in seconds. The
lowered/internal JSON field changed from `ssh.retryDelayMs` to
`ssh.retryDelay`. The default retry delay is now `0.5` seconds.

### Migration Steps

Rename the field and convert milliseconds to seconds:

```diff
- retry_delay_ms = 1000
+ retry_delay = 1.0
```

## 2026-05-17: snake_case virtie manifest

### Who Is Affected

- Direct manifest producers using the previous camelCase JSON/TOML keys.
- Consumers reading `agentspace.sandbox.launch.virtieManifestData` directly.
- Manifests that relied on relative runtime sockets resolving under
  `$XDG_RUNTIME_DIR`.

### What Changed

The supported virtie manifest contract now matches the simplified snake_case
shape documented by `virtie/examples/manifest-full.toml`. Generated JSON uses
the same field names as hand-written TOML.

Historical top-level groups such as `identity`, `paths`, `memory`, and `cpu`
were flattened into backend-neutral fields where possible. QEMU-specific knobs
now live under `qemu`, and VM sizing lives under `machine`.

`runtime_dir` is no longer part of the public manifest. Relative QMP, QGA,
SSH-ready, and virtiofsd socket paths resolve under `state_dir`, which also
contains PID files and suspend state.

### Migration Steps

Rename direct manifest fields to the new shape:

```diff
- "identity": { "hostName": "agent-sandbox" },
- "paths": { "workingDir": ".", "stateDir": ".agentspace" },
- "qemu": { "binaryPath": "/nix/store/.../qemu-system-x86_64" },
- "memory": { "sizeMiB": 4096 },
+ "host_name": "agent-sandbox",
+ "working_dir": ".",
+ "state_dir": ".agentspace",
+ "qemu": { "exec": ["/nix/store/.../qemu-system-x86_64"] },
+ "machine": { "memory": 4096 },
```

Common list-entry renames:

- `volumes[].imagePath -> volumes[].image`
- `volumes[].sizeMiB -> volumes[].size`
- `volumes[].fsType -> volumes[].fs`
- `volumes[].autoCreate -> volumes[].create`
- `volumes[].readOnly -> volumes[].read_only`
- `mounts[].sourcePath -> mounts[].source`
- `mounts[].socketPath -> mounts[].virtiofsd_socket`
- `mounts[].daemon.path + daemon.args -> mounts[].virtiofsd_exec`
- `mounts[].readOnly -> mounts[].read_only`
- `mounts[].securityModel -> mounts[].security_model`
- `writeFiles[].guestPath -> write_files[].guest_path`
- `writeFiles[].path -> write_files[].source`
- `ssh.argv -> ssh.exec`
- `vsock.cidRange.start/end -> vsock.cid_range.min/max`

Use `virtie/examples/manifest-simple.toml` or the annotated
`virtie/examples/manifest-full.toml` as references.

## 2026-05-12: simplified virtie manifest

### Who Is Affected

- Direct manifest producers that emit the previous fully resolved `qemu`
  manifest object.
- Direct manifest producers using path-keyed `writeFiles`.
- Consumers reading generated `agentspace.sandbox.launch.virtieManifestData`
  and expecting resolved QEMU device fields.

### What Changed

The public manifest now carries evaluated launch facts, and `virtie` derives
the concrete host-side QEMU policy from those facts. Nix-generated manifests
remain JSON, but `virtie` also accepts TOML for hand-written manifests.

The previous resolved `qemu` contract was removed. Fields such as machine
options, transport selection, device IDs, block letters, memory backend, QMP and
SSH-ready defaults, and network lowering are now `virtie` policy. The generated
Nix manifest no longer includes `virtiofs.daemons`; managed `virtiofsd` commands
are attached to `mounts[]` entries instead. `writeFiles` is now a list with
`guestPath`, not an object keyed by guest path.

### Migration Steps

Move required kernel paths to top-level `kernel.path` and `kernel.initrdPath`,
describe shares under `mounts[]`, describe volumes under `volumes[]`, and
convert path-keyed write files to list entries:

```diff
- "writeFiles": {
-   "/etc/example.conf": { "text": "hello" }
- }
+ "writeFiles": [
+   { "guestPath": "/etc/example.conf", "text": "hello" }
+ ]
```

Use `virtie/manifest-example-simple.toml` for the smallest hand-written shape
and `virtie/manifest-example-full.toml` for the complete field set.

## 2026-05-12: native ext4 volume image creation

### Who Is Affected

- Direct manifest producers using `volumes[].autoCreate` with a non-`ext4`
  `fsType`.
- Direct manifest producers setting `volumes[].mkfsExtraArgs`.
- Consumers relying on `virtie`'s package wrapper to provide `mkfs.ext4`.

### What Changed

`virtie` now creates auto-created ext4 volume images natively instead of running
`mkfs.<fsType>`. Auto-created volumes only support `fsType = "ext4"`, and
`mkfsExtraArgs` is rejected because no external mkfs command is invoked.
`volumes[].sizeMiB` must be at least `256`, and `volumes[].label` remains
supported.

Nix-generated agentspace manifests no longer emit `mkfsExtraArgs` for managed
volumes, and the `virtie` package no longer adds `e2fsprogs` to `PATH`.

### Migration Steps

Use ext4 for auto-created volumes, set `sizeMiB` to at least `256`, and remove
`mkfsExtraArgs` from direct manifests. If you need a different filesystem,
custom mkfs options, or a smaller image, create the image outside `virtie` and
reference it with `autoCreate = false`.

## 2026-05-08: writeFiles inline content is plaintext text

### Who Is Affected

- Nix consumers setting `agentspace.sandbox.writeFiles.*.content`.
- Direct manifest producers setting `writeFiles.*.content`.
- Consumers writing binary files inline in manifests.

### What Changed

Inline `writeFiles` entries now use `text` instead of `content`.

```diff
  {
    "writeFiles": {
-     "/etc/example.conf": { "content": "cGxhaW4gdGV4dCBieXRlcw==" }
+     "/etc/example.conf": { "text": "plain text bytes" }
    }
  }
```

The `text` value is plaintext JSON string data and is not base64-encoded.
Binary files must be supplied through `path`.

For Nix consumers:

```diff
- agentspace.sandbox.writeFiles."/etc/example.conf".content = "plain text bytes";
+ agentspace.sandbox.writeFiles."/etc/example.conf".text = "plain text bytes";
```

### Migration Steps

Rename inline `content` entries to `text`. Direct manifest producers must decode
old base64 strings to plaintext, or move binary files to `path`.

## 2026-05-07: sandbox machine sizing and SSH retry delay

### Who Is Affected

- Nix consumers configuring VM memory or vCPU count through lower-level
  `microvm` overrides.
- Direct manifest producers that always set `qemu.smp.cpus`.
- Consumers that need to tune transient SSH startup retry timing.

### What Changed

The full sandbox now accepts a public machine sizing interface:

```nix
agentspace.sandbox.machine.memory = 4096; # MiB
agentspace.sandbox.machine.vcpu = null;  # use host-visible CPU count
```

`agentspace.sandbox.machine.memory` defaults to `4096`.
`agentspace.sandbox.machine.vcpu` defaults to `null`. When null, generated
manifests omit `qemu.smp.cpus`, and `virtie` resolves the CPU count with the
host-visible CPU count at launch time.

Generated manifests now also include `ssh.retryDelayMs`, defaulting to `1000`.
`virtie launch` uses this value as the delay before retrying transient SSH
startup failures.

### Migration Steps

Update full sandbox sizing that previously overrode microvm options directly:

```diff
- extraModules = [{ microvm.mem = 512; microvm.vcpu = 2; }];
+ machine.memory = 512;
+ machine.vcpu = 2;
```

Set `machine.vcpu = null` or omit it to use the runtime host-visible CPU count.
Set `agentspace.sandbox.ssh.retryDelayMs = 0` to retry immediately, or a larger
millisecond value to slow retries down.

## 2026-04-28: SSH autoconnect is explicit

### Who Is Affected

- Direct users of `virtie launch` that expect it to attach an SSH session.
- Consumers configuring `agentspace.sandbox.command`,
  `agentspace.sandbox.sshAuthorizedKeys`, or
  `agentspace.sandbox.sshIdentityFile`.
- Tooling that inspects generated launch wrapper command lines.

### What Changed

`virtie launch` no longer attaches SSH by default. Use `virtie launch --ssh`
to attach the configured SSH session or run a remote command. Without `--ssh`,
launch starts the VM, prints an out-of-band SSH command after readiness, and
blocks until the VM exits or is suspended.

The Nix option `agentspace.sandbox.command` moved to
`agentspace.sandbox.ssh.command`, and `agentspace.sandbox.sshAuthorizedKeys`
moved to `agentspace.sandbox.ssh.authorizedKeys`. The Nix option
`agentspace.sandbox.sshIdentityFile` moved to
`agentspace.sandbox.ssh.identityFile`. The generated launch wrapper now uses
`agentspace.sandbox.ssh.autoconnect`, defaulting to `true`, to preserve the
existing wrapper behavior.

### Migration Steps

Update direct `virtie launch` calls that should attach SSH:

```diff
- virtie launch --manifest="$manifest"
+ virtie launch --ssh --manifest="$manifest"
```

Update sandbox command configuration:

```diff
- agentspace.sandbox.command = "bash -lc pwd";
+ agentspace.sandbox.ssh.command = "bash -lc pwd";
```

Set `agentspace.sandbox.ssh.autoconnect = false` when the generated wrapper
should start the VM and leave SSH connection out of band.

Update sandbox authorized key configuration:

```diff
- agentspace.sandbox.sshAuthorizedKeys = keys;
+ agentspace.sandbox.ssh.authorizedKeys = keys;
```

Update sandbox identity file configuration:

```diff
- agentspace.sandbox.sshIdentityFile = "./id_ed25519";
+ agentspace.sandbox.ssh.identityFile = "./id_ed25519";
```

## 2026-04-28: virtie notification hooks

### Who Is Affected

- Nix consumers that want host-side notifications for runtime events.
- Consumers that generate or validate virtie manifests directly.

### What Changed

Generated manifests now include a `notifications` object. Notifications are
disabled by default when no command is configured. The manifest accepts one
optional command and an optional state allowlist:

```json
{
  "notifications": {
    "command": {
      "path": "/run/current-system/sw/bin/sh",
      "args": ["-c", "notify-send \"virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE\""]
    },
    "states": ["runtime:resume", "runtime:suspend", "balloon:resize"]
  }
}
```

`states = []` or an omitted state list means all notification states. Hook
failures are logged and ignored. Virtie passes command args unchanged.

The hook environment includes `VIRTIE_NOTIFY_STATE`,
`VIRTIE_NOTIFY_MESSAGE`, and context values as
`VIRTIE_NOTIFY_CONTEXT_<UPPER_SNAKE_KEY>`.

For Nix users, the equivalent option is:

```nix
agentspace.sandbox.notifications.command =
  ''notify-send "virtie: $VIRTIE_NOTIFY_STATE - $VIRTIE_NOTIFY_MESSAGE"'';
agentspace.sandbox.notifications.states = [
  "runtime:resume"
  "runtime:suspend"
  "balloon:resize"
];
```

### Migration Steps

Nix consumers should replace `agentspace.sandbox.notifications.command.path`
and `agentspace.sandbox.notifications.command.args` with the shell command
string in `agentspace.sandbox.notifications.command`. Set it to `""` or omit it
to keep notifications disabled.

Direct manifest producers should omit `notifications.command` to keep
notifications disabled, or include both `notifications.command.path` and any
desired args.

## 2026-04-27: manifest writeFiles and guest-agent socket

### Who Is Affected

- Consumers that generate or validate virtie manifests directly.
- Nix consumers that want boot-time file injection into a fresh guest.

### What Changed

Generated manifests now include `qemu.guestAgent.socketPath`, resolved with the
same runtime-directory policy as QMP and virtiofs sockets. The sandbox module
also enables the in-guest QEMU guest agent service.

The manifest accepts an optional `writeFiles` map:

```json
{
  "writeFiles": {
    "/etc/example.conf": { "text": "plain text bytes", "chown": "agent:users", "mode": "0640" },
    "/etc/from-host": { "path": "relative-or-absolute-host-path" }
  }
}
```

Exactly one of `text` or `path` is required for each entry. `text` is
plaintext JSON string content for non-binary files. Binary files must use
`path`. Relative host `path` values resolve against `paths.workingDir`; guest
target paths must be absolute.

Each entry may also set `chown` to a guest ownership string such as
`"agent:users"` and `mode` to a four-digit octal string such as `"0640"`.
Before writing a file, `virtie launch` checks whether the destination parent
directory already exists. Existing parent directories are left unchanged; when
the parent is missing, `virtie launch` creates it with `install -d`, passing
user and group parts from `chown` when present. When present, `virtie launch`
applies ownership and then mode with the QEMU guest agent after the file is
written and closed. Directory creation, chown, and chmod failures are fatal.

For Nix users, the equivalent option is:

```nix
agentspace.sandbox.writeFiles."/etc/example.conf".text = "plain text bytes";
agentspace.sandbox.writeFiles."/etc/example.conf".chown = "agent:users";
agentspace.sandbox.writeFiles."/etc/example.conf".mode = "0640";
agentspace.sandbox.writeFiles."/etc/from-host".path = "relative-host-path";
```

The Nix option emits `text` directly into the generated manifest. It no longer
base64-encodes inline file content.

`virtie launch` writes these files after QMP readiness and guest-agent ping, and
before SSH readiness. File injection runs only for fresh launches; restores via
`virtie launch --resume=auto` or `--resume=force` skip `writeFiles`.

### Migration Steps

No migration is required for consumers that do not set `writeFiles`. Direct
manifest producers should rename inline `writeFiles.*.content` entries to
`writeFiles.*.text` and decode any existing base64 strings to plaintext. Nix
consumers should rename `agentspace.sandbox.writeFiles.*.content` to
`agentspace.sandbox.writeFiles.*.text`. Binary files should move to `path`.
Direct manifest producers should include `qemu.guestAgent.socketPath` when
using `writeFiles`.

## 2026-04-27: launch resume modes replace `virtie resume`

### Who Is Affected

- Direct users of `virtie resume`.
- Tooling that restores saved virtie sessions.

### What Changed

`virtie resume --manifest=MANIFEST` has been removed. Restores now use the
shared launch lifecycle:

```console
virtie launch --resume=force --manifest=MANIFEST
```

`virtie launch` also accepts `--resume=no` for fresh launch and `--resume=auto`
to restore when valid saved state exists, otherwise launch fresh. The default is
`--resume=no`.

### Migration Steps

Replace:

```console
virtie resume --manifest=MANIFEST
```

with:

```console
virtie launch --resume=force --manifest=MANIFEST
```

## 2026-04-27: saved-state suspend is the only suspend mode

### Who Is Affected

- Direct users of `virtie suspend`.
- Tooling that relied on live pause/resume state or the suspend exit flag.

### What Changed

`virtie suspend --manifest=MANIFEST` now saves QEMU migration state to disk,
exits the launch session, and writes saved suspend state. The old live
pause/resume behavior has been removed.

The external command now sends `SIGTSTP` to the launch process as a caught
control signal. It is not terminal/job-control suspend, and `SIGCONT` resume is
not supported.

The suspend exit flag has been removed. Restoring now goes through
`virtie launch --resume=force --manifest=MANIFEST`; it no longer signals a live
launch process or clears live paused state.

### Migration Steps

Replace suspend calls that passed the old exit flag with:

```console
virtie suspend --manifest=MANIFEST
```

Do not use `virtie launch --resume=force` unless saved suspend state exists.

## 2026-04-27: persistence state directory and resume workspace

### Who Is Affected

- Tooling that reads virtie PID, suspend, or VM state files directly.
- Direct users restoring a session from a different directory than
  the original launch.

### What Changed

Virtie runtime state now lives directly under the manifest persistence base
directory. With the default `agentspace.sandbox.persistence.baseDir = ".agentspace"`,
files such as `<hostName>.pid`, `<hostName>.suspend.json`, and `<hostName>.vmstate`
are written to `.agentspace/` instead of `.agentspace/.virtie/`.

`virtie launch` also rewrites the copied runtime manifest so
`paths.workingDir` is the absolute launch workspace. `virtie launch --resume`
uses that manifest path to restore the original workspace share, even if it is
invoked from another current working directory.

### Migration Steps

Update tooling to read virtie state files from `<basedir>/` and continue
passing the copied runtime manifest, for example
`.agentspace/virtie-<hostName>.json`, to `virtie suspend` and
`virtie launch --resume=force`.

## 2026-04-26: sandbox launch command option

### Who Is Affected

- Consumers that want `nix run`/`mkLaunch` to start a specific in-guest command
  without passing command arguments at the shell each time.
- Tooling that inspects generated launch wrapper command lines.

### What Changed

`agentspace.sandbox.command` is now available as a string. The generated launch
wrapper passes it to `virtie launch` as the remote SSH command when the wrapper
is invoked without command arguments. Arguments supplied to the wrapper override
the configured command for that launch.

### Migration Steps

No migration is required. The default is `""`, preserving the previous
interactive session behavior.

## 2026-04-26: persistence base directory and disk suspend

### Who Is Affected

- Consumers relying on default persistence image paths in the repository root.
- Tooling that reads generated manifest or wrapper paths directly.
- Direct users of `virtie suspend`/`virtie resume`.

### What Changed

`agentspace.sandbox.persistence.baseDir` defaults to `.agentspace`. Relative
`persistence.homeImage` and `persistence.storeOverlay` paths are now generated
under that base directory. The generated launch wrapper also copies its manifest
template to `<basedir>/virtie-<hostName>.json` at runtime and launches `virtie`
with that mutable workspace path.

`virtie suspend --manifest=MANIFEST` saves QEMU migration state under the
manifest persistence state directory, exits the launch session, and leaves
`virtie resume --manifest=MANIFEST` able to restore the saved VM when no live
launch PID is valid.

### Migration Steps

To keep root-level generated persistence files, set:

```nix
agentspace.sandbox.persistence.baseDir = ".";
```

Tooling that invokes `virtie suspend` or `virtie resume` should use the runtime
manifest path generated by the launch wrapper, not the Nix-store template path.

## 2026-04-26: `virtie` manifest flag and suspend/resume commands

### Who Is Affected

- Consumers invoking `virtie launch` directly.
- Consumers inspecting generated launch wrapper scripts.
- Tooling that shells out to `virtie` with the old positional manifest form.

Normal `nix run` users that only call the generated launch wrapper do not need
to change their command line; the wrapper now passes the manifest flag for them.

### What Changed

`virtie launch` now requires an explicit `--manifest=MANIFEST` option instead
of a positional manifest path.

Before:

```console
virtie launch MANIFEST [-- <remote-cmd...>]
```

After:

```console
virtie launch --manifest=MANIFEST [-- <remote-cmd...>]
```

Two new lifecycle commands are available for the active QEMU process:

```console
virtie suspend --manifest=MANIFEST
virtie resume --manifest=MANIFEST
```

At the time these commands were introduced, suspend state used the default
location:

```text
<workingDir>/.virtie/<hostName>.suspend.json
```

### Migration Steps

Update direct `virtie launch` calls:

```diff
- virtie launch "$manifest" -- "$@"
+ virtie launch --manifest="$manifest" -- "$@"
```

If tooling parses generated launch wrappers, update checks from the old
positional shape to the `--manifest=` shape.
