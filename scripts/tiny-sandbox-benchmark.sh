set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: tiny-sandbox-benchmark [--iterations N] [--out FILE] BEFORE_REF AFTER_REF

Benchmarks tiny sandbox size and boot-to-SSH timing for two flake refs plus
a full mkSandbox baseline from AFTER_REF. Run from the repository root. A
typical local comparison is:

  tiny-sandbox-benchmark "git+file://$PWD?rev=$(git rev-parse HEAD)" "path:$PWD"

The BEFORE ref is instantiated with the pre-feature-compatible tiny config.
The AFTER ref is instantiated with workspace virtiofs defaults plus one
writeFiles entry. The full baseline uses mkSandbox with the same SSH,
workspace, and writeFiles-facing options, but keeps full-sandbox storage and
uses 4096 MiB of guest memory.
USAGE
}

iterations=3
out_file=tiny-sandbox-benchmark.tsv

while [ "$#" -gt 0 ]; do
  case "$1" in
    --iterations)
      iterations="$2"
      shift 2
      ;;
    --out)
      out_file="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    -*)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      break
      ;;
  esac
done

if [ "$#" -ne 2 ]; then
  usage >&2
  exit 2
fi

before_ref="$1"
after_ref="$2"

case "$iterations" in
  ''|*[!0-9]*)
    echo "--iterations must be a positive integer" >&2
    exit 2
    ;;
esac
if [ "$iterations" -lt 1 ]; then
  echo "--iterations must be at least 1" >&2
  exit 2
fi

public_key='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDb0Rki6+vyVxLBS28GTL2XfE6UCbpus0duCx8l2vjjF agentspace-tiny-benchmark'
private_key='-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACA29EZIuvr8lcSwUtvBky9l3xOlAm6brNHbgsfJdr44xQAAAJjTv2Ip079i
KQAAAAtzc2gtZWQyNTUxOQAAACA29EZIuvr8lcSwUtvBky9l3xOlAm6brNHbgsfJdr44xQ
AAAEB/Hj0k59Nf3i7ZlAmbbqJilR0evigNBxRiHYny4TgLWTb0Rki6+vyVxLBS28GTL2Xf
E6UCbpus0duCx8l2vjjFAAAAE2FnZW50c3BhY2UtdGlueS1lMmUBAg==
-----END OPENSSH PRIVATE KEY-----'

nix_flags=(--extra-experimental-features 'nix-command flakes' --impure)

expr_for() {
  local ref="$1"
  local label="$2"
  local profile="$3"
  local mode="$4"
  local output="$5"
  local constructor memory launch_attr persistence_dir root_override write_files

  case "$profile" in
    tiny)
      constructor=mkTinySandbox
      memory=192
      launch_attr=tinySandbox
      persistence_dir=".agentspace-tiny-benchmark-$label"
      root_override='
        fileSystems."/".options = lib.mkForce [
          "mode=0755"
          "size=32M"
        ];
      '
      ;;
    full)
      constructor=mkSandbox
      memory=4096
      launch_attr=sandbox
      persistence_dir=".agentspace-full-benchmark-$label"
      root_override=''
      ;;
    *)
      echo "unknown benchmark profile: $profile" >&2
      exit 2
      ;;
  esac

  if [ "$mode" = feature ]; then
    write_files='
      writeFiles."/tmp/tiny-sandbox-benchmark" = {
        content = "dGlueS1zYW5kYm94LWJlbmNobWFyaw==";
        mode = "0644";
      };
    '
  else
    write_files=''
  fi

  cat <<NIX
let
  flake = builtins.getFlake "$ref";
  system = "x86_64-linux";
  pkgs = flake.inputs.nixpkgs.legacyPackages.\${system};
  lib = flake.inputs.nixpkgs.lib;
  vm = flake.lib.$constructor {
    hostName = "agent-$label-benchmark";
    machine = {
      memory = $memory;
      vcpu = 1;
    };
    ssh.authorizedKeys = [ "$public_key" ];
    ssh.identityFile = "id_ed25519";
    persistence.basedir = "$persistence_dir";
    $write_files
    extraModules = [
      {
        microvm.cpu = "max";
        microvm.virtiofsd.group = lib.mkForce null;
        $root_override
      }
    ];
  };
  launch = flake.lib.mkLaunch vm;
in
  if "$output" == "toplevel" then
    vm.config.system.build.toplevel
  else if "$output" == "initrd" then
    vm.config.agentspace.$launch_attr.launch.virtieManifestData.qemu.kernel.initrdPath
  else if "$output" == "launch" then
    pkgs.runCommand "tiny-sandbox-benchmark-launch-$label" { } ''
      mkdir -p \$out/bin
      ln -s \${launch} \$out/bin/launch-agent
    ''
  else
    throw "unknown benchmark output"
NIX
}

build_toplevel() {
  nix build "${nix_flags[@]}" --no-link --print-out-paths --expr "$(expr_for "$1" "$2" "$3" "$4" toplevel)"
}

eval_initrd() {
  nix eval "${nix_flags[@]}" --raw --expr "$(expr_for "$1" "$2" "$3" "$4" initrd)"
}

build_launch() {
  nix build "${nix_flags[@]}" --no-link --print-out-paths --expr "$(expr_for "$1" "$2" "$3" "$4" launch)"
}

closure_size() {
  nix path-info "${nix_flags[@]}" --closure-size "$1" | awk '{ print $2 }'
}

file_size() {
  stat -c %s "$1"
}

record_size() {
  local label="$1"
  local ref="$2"
  local profile="$3"
  local mode="$4"
  local toplevel initrd

  echo "building $label toplevel..." >&2
  toplevel="$(build_toplevel "$ref" "$label" "$profile" "$mode")"
  initrd="$(eval_initrd "$ref" "$label" "$profile" "$mode")"

  {
    printf '%s\tsize\ttoplevel_closure_bytes\t%s\n' "$label" "$(closure_size "$toplevel")"
    printf '%s\tsize\tinitrd_closure_bytes\t%s\n' "$label" "$(closure_size "$initrd")"
    printf '%s\tsize\tinitrd_file_bytes\t%s\n' "$label" "$(file_size "$initrd")"
  } >>"$out_file"
}

record_boot() {
  local display_label="$1"
  local label="$2"
  local ref="$3"
  local profile="$4"
  local mode="$5"
  local launch_dir tmpdir workspace launch_log start_ns end_ns elapsed_ms stats lock_pid

  launch_dir="$(build_launch "$ref" "$label" "$profile" "$mode")"
  tmpdir="$(mktemp -d)"
  workspace="$tmpdir/workspace"
  launch_log="$tmpdir/$label.log"
  mkdir -p "$workspace"
  install -m 0600 /dev/stdin "$workspace/id_ed25519" <<<"$private_key"

  export XDG_RUNTIME_DIR="$tmpdir/run"
  mkdir -p "$XDG_RUNTIME_DIR"
  mkdir -p /tmp/agentspace-vsock
  (
    flock -n 9
    sleep 90
  ) 9>/tmp/agentspace-vsock/3.lock &
  lock_pid=$!

  start_ns="$(date +%s%N)"
  if ! (
    cd "$workspace"
    timeout 70s "$launch_dir/bin/launch-agent" sh -c 'printf tiny-sandbox-benchmark-ok'
  ) >"$launch_log" 2>&1; then
    echo "benchmark launch for $label failed" >&2
    cat "$launch_log" >&2
    kill "$lock_pid" 2>/dev/null || true
    wait "$lock_pid" 2>/dev/null || true
    rm -rf "$tmpdir"
    exit 1
  fi
  end_ns="$(date +%s%N)"

  if ! grep -F 'tiny-sandbox-benchmark-ok' "$launch_log" >/dev/null; then
    echo "benchmark launch for $label did not complete successfully" >&2
    cat "$launch_log" >&2
    kill "$lock_pid" 2>/dev/null || true
    wait "$lock_pid" 2>/dev/null || true
    rm -rf "$tmpdir"
    exit 1
  fi

  elapsed_ms="$(((end_ns - start_ns) / 1000000))"
  stats="$(grep -F 'stats:' "$launch_log" | tail -n 1 | sed 's/^.*stats: //')"
  {
    printf '%s\tboot\telapsed_ms\t%s\n' "$display_label" "$elapsed_ms"
    printf '%s\tboot\tvirtie_stats\t%s\n' "$display_label" "$stats"
  } >>"$out_file"

  kill "$lock_pid" 2>/dev/null || true
  wait "$lock_pid" 2>/dev/null || true
  rm -rf "$tmpdir"
}

requested_out="$(realpath -m "$out_file")"
tmp_out="$(mktemp)"
trap 'rm -f "$tmp_out"' EXIT
: >"$tmp_out"
out_file="$tmp_out"

record_size tiny-before "$before_ref" tiny compat
record_size tiny-after "$after_ref" tiny feature
record_size full "$after_ref" full feature

if [ ! -e /dev/vhost-vsock ]; then
  {
    printf '%s\tboot\tskipped\t/dev/vhost-vsock is not visible\n' tiny-before
    printf '%s\tboot\tskipped\t/dev/vhost-vsock is not visible\n' tiny-after
    printf '%s\tboot\tskipped\t/dev/vhost-vsock is not visible\n' full
  } >>"$out_file"
else
  for i in $(seq 1 "$iterations"); do
    record_boot "tiny-before-$i" "b$i" "$before_ref" tiny compat
    record_boot "tiny-after-$i" "a$i" "$after_ref" tiny feature
    record_boot "full-$i" "f$i" "$after_ref" full feature
  done
fi

cp "$tmp_out" "${TINY_SANDBOX_BENCHMARK_OUT:-$requested_out}"
cat "$tmp_out"
