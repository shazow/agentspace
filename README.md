# agentspace

Secure, recursive container environment for AI coding agents.

## Overview

This Nix flake creates sandboxed containers for running coding agents with:
- Network isolation via slirp4netns
- Optional gVisor runtime for enhanced security (disabled when nested)
- Nix available inside the container for reproducible builds
- Support for recursive nesting (containers within containers)

## Quick Start

```bash
# Enter the sandbox
nix develop

# Or run directly
nix run
```

## Recursive Containers

The sandbox can spawn nested containers when `withPrivileges = true`. This is useful for:
- Running agent workloads that need their own isolated environments
- Testing container configurations from within a container
- Multi-level isolation for untrusted code

### How It Works

1. **First level**: Uses gVisor (`runsc`) for maximum isolation
2. **Nested levels**: Automatically detects containerization and falls back to `runc`
3. **Nix works at all levels**: Configured with `sandbox = false` for container compatibility

### Security Trade-offs

| Mode | Capabilities | Use Case |
|------|-------------|----------|
| `withPrivileges = false` (default) | None (all dropped) | Running trusted agent code |
| `withPrivileges = true` | SYS_ADMIN, FOWNER, CHOWN, SETUID, SETGID | Nested containers, Nix builds |

### Configuration

Edit `flake.nix` to customize the sandbox:

```nix
sandbox = mkSandbox {
  inherit pkgs system;
  packages = with pkgs; [ /* your packages */ ];
  withPrivileges = true;  # Enable nested containers
  withNix = true;         # Include Nix in container (default)
  withNixVolume = false;  # Persist /nix across runs
};
```

## Outputs

| Output | Description |
|--------|-------------|
| `nix run` / `nix develop` | Launch interactive sandbox |
| `nix build .#image` | Build container image tarball |
| `nix build .#vm` | Build QCOW2 VM image |
| `nix run .#devcontainer` | Setup VS Code devcontainer |

## Extending

Use `lib.mkSandbox` to create custom sandbox configurations:

```nix
{
  inputs.agentspace.url = "github:shazow/agentspace";
  
  outputs = { self, agentspace, nixpkgs, ... }:
    let
      pkgs = nixpkgs.legacyPackages.x86_64-linux;
      sandbox = agentspace.lib.mkSandbox {
        inherit pkgs;
        system = "x86_64-linux";
        packages = with pkgs; [ python3 ];
        withPrivileges = true;
      };
    in {
      packages.x86_64-linux.default = sandbox.runContainer;
    };
}
```

## Requirements

- Nix with flakes enabled
- Podman (for container runtime)
- Linux (gVisor requires Linux; macOS uses runc)
