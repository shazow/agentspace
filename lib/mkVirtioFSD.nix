{ pkgs, lib }:

{
  hostName,
  package,
  group ? null,
  threadPoolSize,
  inodeFileHandles ? null,
  extraArgs ? [ ],
  hypervisor ? "qemu",
}:
{
  tag,
  socket,
  source,
  readOnly ? false,
  cache,
  ...
}:
pkgs.writeShellScript "virtiofsd-${hostName}-${tag}" ''
  # Keep virtiofsd's default namespace sandbox, but make the namespace setup
  # explicit for normal users. Without these maps, virtiofsd tries to set the
  # sandboxed process uid/gid to root and emits warnings when launched without
  # the required host privileges.
  if [ "$(id -u)" != 0 ]; then
    opt_userns=("--uid-map=:0:$(id -u):1:" "--gid-map=:0:$(id -g):1:")
  else
    opt_userns=()
  fi

  # virtiofsd defaults to a large nofile limit and warns when the inherited hard
  # limit is lower. Ask for the current hard limit instead; fall back to 0
  # ("leave unchanged") for non-numeric shells such as "unlimited".
  hard_nofile=$(ulimit -Hn)
  if [[ "$hard_nofile" =~ ^[0-9]+$ ]]; then
    opt_rlimit=(--rlimit-nofile "$hard_nofile")
  else
    opt_rlimit=(--rlimit-nofile 0)
  fi

  # File handles reduce fd pressure, but probing them without
  # CAP_DAC_READ_SEARCH produces warnings before virtiofsd falls back. Use
  # prefer only when the capability is effective; otherwise skip the probe.
  opt_inode_file_handles=(--inode-file-handles=never)
  cap_eff=$(${pkgs.gawk}/bin/awk '/^CapEff:/ { print $2 }' /proc/self/status 2>/dev/null || true)
  if [[ "$cap_eff" =~ ^[0-9A-Fa-f]+$ ]] && (( (16#$cap_eff & (1 << 2)) != 0 )); then
    opt_inode_file_handles=(--inode-file-handles=prefer)
  fi
  ${lib.optionalString (
    inodeFileHandles != null
  ) "opt_inode_file_handles=(--inode-file-handles=${inodeFileHandles})"}

  socket_path=${lib.escapeShellArg socket}
  if [ -n "''${VIRTIOFSD_SOCKET-}" ]; then
    socket_path="$VIRTIOFSD_SOCKET"
  fi

  exec ${lib.getExe package} \
    --socket-path="$socket_path" \
    ${lib.optionalString (group != null) "--socket-group=${group}"} \
    --shared-dir="''${VIRTIOFSD_SOURCE-${lib.escapeShellArg source}}" \
    "''${opt_userns[@]}" \
    "''${opt_rlimit[@]}" \
    --thread-pool-size ${toString threadPoolSize} \
    --posix-acl --xattr \
    --cache=${cache} \
    "''${opt_inode_file_handles[@]}" \
    ${lib.optionalString (hypervisor == "crosvm") "--tag=${tag}"} \
    ${lib.optionalString readOnly "--readonly"} \
    ${lib.escapeShellArgs extraArgs}
''
