#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd -- "$script_dir/.." && pwd)"

work_dir="${VIRTIE_ALPINE_DIR:-$repo_dir/.virtie-alpine}"
mirror="${ALPINE_MIRROR:-https://dl-cdn.alpinelinux.org}"
release="${ALPINE_RELEASE:-latest-stable}"
arch="${ALPINE_ARCH:-x86_64}"
memory_mib="${VIRTIE_ALPINE_MEMORY_MIB:-1024}"
cpus="${VIRTIE_ALPINE_CPUS:-2}"
disk_mib="${VIRTIE_ALPINE_DISK_MIB:-1024}"

netboot_url="$mirror/alpine/$release/releases/$arch/netboot"
repo_url="$mirror/alpine/$release/main"
modloop_url="$netboot_url/modloop-virt"

kernel="$work_dir/vmlinuz-virt"
initrd="$work_dir/initramfs-virt"
modloop="$work_dir/modloop-virt"
disk="$work_dir/alpine.raw"
manifest="$work_dir/virtie-alpine.json"

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$1" >&2
    exit 1
  fi
}

download() {
  local url="$1"
  local dest="$2"

  if [ -s "$dest" ]; then
    printf 'using cached %s\n' "$dest" >&2
    return
  fi

  printf 'downloading %s\n' "$url" >&2
  curl -fL --retry 3 --retry-delay 2 -o "$dest.tmp" "$url"
  mv "$dest.tmp" "$dest"
}

require curl

mkdir -p "$work_dir"

download "$netboot_url/vmlinuz-virt" "$kernel"
download "$netboot_url/initramfs-virt" "$initrd"
download "$netboot_url/modloop-virt" "$modloop"

if [ ! -e "$disk" ]; then
  printf 'creating raw disk %s (%s MiB)\n' "$disk" "$disk_mib" >&2
  truncate -s "${disk_mib}M" "$disk"
fi

if command -v qemu-system-x86_64 >/dev/null 2>&1; then
  qemu_binary="$(command -v qemu-system-x86_64)"
elif command -v nix >/dev/null 2>&1; then
  mkdir -p "$work_dir/bin"
  qemu_binary="$work_dir/bin/qemu-system-x86_64"
  cat >"$qemu_binary" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exec nix shell nixpkgs#qemu -c qemu-system-x86_64 "$@"
EOF
  chmod +x "$qemu_binary"
else
  printf 'missing required command: qemu-system-x86_64\n' >&2
  exit 1
fi

ssh_binary="$(command -v ssh || printf '/usr/bin/ssh')"
if [ -r /dev/kvm ] && [ -w /dev/kvm ]; then
  machine_accel="${QEMU_ACCEL:-kvm:tcg}"
  cpu_model="${QEMU_CPU_MODEL:-host}"
  enable_kvm=true
else
  machine_accel="${QEMU_ACCEL:-tcg}"
  cpu_model="${QEMU_CPU_MODEL:-max}"
  enable_kvm=false
fi

cat >"$manifest" <<EOF
{
  "identity": {
    "hostName": "alpine-vm"
  },
  "paths": {
    "workingDir": "$work_dir",
    "lockPath": "$work_dir/alpine-vm.lock",
    "runtimeDir": ""
  },
  "persistence": {
    "directories": ["$work_dir"],
    "baseDir": "$work_dir",
    "stateDir": "$work_dir"
  },
  "ssh": {
    "argv": ["$ssh_binary", "-q"],
    "user": "alpine"
  },
  "qemu": {
    "binaryPath": "$qemu_binary",
    "name": "alpine-vm",
    "machine": {
      "type": "microvm",
      "options": [
        "accel=$machine_accel",
        "mem-merge=on",
        "acpi=on",
        "pit=off",
        "pic=off",
        "pcie=on",
        "rtc=on",
        "usb=off"
      ]
    },
    "cpu": {
      "model": "$cpu_model",
      "enableKvm": $enable_kvm
    },
    "memory": {
      "sizeMiB": $memory_mib
    },
    "kernel": {
      "path": "$kernel",
      "initrdPath": "$initrd",
      "params": "console=ttyS0 modules=loop,squashfs,sd-mod,virtio_pci,virtio_blk,virtio_net quiet nomodeset ip=dhcp alpine_repo=$repo_url modloop=$modloop_url"
    },
    "smp": {
      "cpus": $cpus
    },
    "console": {
      "stdioChardev": true,
      "serialConsole": true
    },
    "knobs": {
      "noDefaults": true,
      "noUserConfig": true,
      "noReboot": true,
      "noGraphic": true
    },
    "qmp": {
      "socketPath": "qmp.sock"
    },
    "devices": {
      "rng": {
        "id": "rng0",
        "transport": "pci"
      },
      "block": [
        {
          "id": "vda",
          "imagePath": "$disk",
          "aio": "threads",
          "transport": "pci"
        }
      ],
      "network": [
        {
          "id": "net0",
          "backend": "user",
          "macAddress": "02:02:00:00:00:01",
          "transport": "pci"
        }
      ],
      "vsock": {
        "id": "vsock0",
        "transport": "pci"
      }
    }
  },
  "virtiofs": {
    "daemons": []
  }
}
EOF

printf 'wrote manifest %s\n' "$manifest" >&2
printf 'launching Alpine with virtie\n' >&2

if [ -n "${VIRTIE_BIN:-}" ]; then
  exec "$VIRTIE_BIN" launch --resume=no --manifest="$manifest"
elif command -v virtie >/dev/null 2>&1; then
  exec virtie launch --resume=no --manifest="$manifest"
else
  cd "$repo_dir"
  exec nix run .#virtie -- launch --resume=no --manifest="$manifest"
fi
