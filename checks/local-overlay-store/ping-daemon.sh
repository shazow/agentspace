#!/bin/sh
set -eu

export NIX_REMOTE=daemon
/run/current-system/sw/bin/nix store ping
