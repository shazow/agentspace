#!/bin/sh
set -eu

export NIX_REMOTE=daemon
nix=/run/current-system/sw/bin/nix
payload=/tmp/agentspace-local-overlay-canary
printf "%s\n" agentspace-local-overlay-canary > "$payload"
canary_path=$($nix store add-file "$payload")
upper_path=/nix/.rw-store/store/${canary_path##*/}

test -e "$canary_path"
test -e "$upper_path"
$nix path-info "$canary_path"
sync
printf "CANARY_PATH=%s\n" "$canary_path"
