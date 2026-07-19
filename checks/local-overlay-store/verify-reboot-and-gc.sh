#!/bin/sh
set -eu

export NIX_REMOTE=daemon
nix=/run/current-system/sw/bin/nix
canary_path=$1
canary_name=${canary_path##*/}
upper_path=/nix/.rw-store/store/$canary_name
deleted_paths=/tmp/agentspace-bounded-gc-deletions
lower_probe=$(readlink -f /run/current-system/sw/bin/sh)
lower_name=${lower_probe#/nix/store/}
lower_name=${lower_name%%/*}
lower_physical=/nix/.ro-store/$lower_name

snapshot_upper() {
  find /nix/.rw-store/store -mindepth 1 -maxdepth 1 \
    ! -name "$canary_name" -printf "%f:%y\n" | sort
}

assert_lower_probe_visible() {
  phase=$1
  if [ ! -e "$lower_physical" ] || [ ! -e "/nix/store/$lower_name" ]; then
    echo "$phase hid the lower store probe" >&2
    exit 1
  fi
}

assert_store_layers_unchanged() {
  phase=$1
  upper_after=$(snapshot_upper)
  if [ "$upper_before" != "$upper_after" ]; then
    echo "$phase made unexpected changes to the upper layer" >&2
    echo "upper entries before GC, excluding the canary:" >&2
    printf "%s\n" "$upper_before" | sed "s/^/  /" >&2
    echo "upper entries after GC:" >&2
    printf "%s\n" "$upper_after" | sed "s/^/  /" >&2
    exit 1
  fi
  assert_lower_probe_visible "$phase"
}

case "$lower_probe" in
  /nix/store/*) ;;
  *)
    echo "could not resolve a lower store probe: $lower_probe" >&2
    exit 1
    ;;
esac
assert_lower_probe_visible "pre-GC state"

upper_before=$(snapshot_upper)
if ! $nix path-info "$canary_path"; then
  echo "metadata for the upper store path did not survive reboot" >&2
  exit 1
fi

if ! bounded_gc_output=$($nix store gc --max 1M 2>&1); then
  printf "%s\n" "$bounded_gc_output" >&2
  echo "bounded guest store garbage collection failed" >&2
  exit 1
fi
printf "%s\n" "$bounded_gc_output" >&2

printf "%s\n" "$bounded_gc_output" \
  | sed -n "s|^deleting .\(/nix/store/.*\).$|\1|p" \
  > "$deleted_paths"
while IFS= read -r deleted_path; do
  deleted_name=${deleted_path#/nix/store/}
  deleted_name=${deleted_name%%/*}
  if [ -e "/nix/.ro-store/$deleted_name" ] \
    && [ ! -e "/nix/store/$deleted_name" ]; then
    echo "bounded garbage collection hid lower path $deleted_path" >&2
    exit 1
  fi
done < "$deleted_paths"

assert_store_layers_unchanged "bounded garbage collection"

if ! $nix store gc; then
  echo "full guest store garbage collection failed" >&2
  exit 1
fi

assert_store_layers_unchanged "full garbage collection"

if [ -e "$canary_path" ] || [ -e "$upper_path" ]; then
  echo "garbage collection left the unrooted upper store path behind" >&2
  exit 1
fi

if $nix path-info "$canary_path" >/dev/null 2>&1; then
  echo "garbage collection left the canary metadata registered" >&2
  exit 1
fi

sync
