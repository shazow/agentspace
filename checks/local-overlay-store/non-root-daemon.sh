#!/bin/sh
set -eu

export NIX_REMOTE=daemon
nix=/run/current-system/sw/bin/nix
payload=/tmp/agentspace-non-root-overlay-canary
printf "%s\n" agentspace-non-root-overlay-canary > "$payload"
canary_path=$($nix store add-file "$payload")

for writable_path in \
  /nix/.rw-store/state \
  /nix/.local-overlay-lower-store \
  /nix/.local-overlay-lower-store/.links; do
  if [ "$(stat -c "%U:%G" "$writable_path")" != nix-daemon:nix-daemon ]; then
    echo "$writable_path is not owned by nix-daemon:nix-daemon" >&2
    exit 1
  fi
done

test -e "$canary_path"
test -e "/nix/.rw-store/store/${canary_path##*/}"
$nix path-info "$canary_path"
