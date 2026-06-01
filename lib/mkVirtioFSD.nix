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
  # virtiofsd defaults to a large nofile limit and warns when the inherited hard
  # limit is lower. Ask for the current hard limit instead; fall back to 0
  # ("leave unchanged") for non-numeric shells such as "unlimited".
  hard_nofile=$(ulimit -Hn)
  if [[ "$hard_nofile" =~ ^[0-9]+$ ]]; then
    opt_rlimit=(--rlimit-nofile "$hard_nofile")
  else
    opt_rlimit=(--rlimit-nofile 0)
  fi

  # File handle probing can warn depending on host filesystem and runtime
  # privileges. Keep managed launches quiet by default; users can still opt in
  # with microvm.virtiofsd.inodeFileHandles.
  opt_inode_file_handles=(--inode-file-handles=never)
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
    "''${opt_rlimit[@]}" \
    --thread-pool-size ${toString threadPoolSize} \
    --posix-acl --xattr \
    --cache=${cache} \
    "''${opt_inode_file_handles[@]}" \
    ${lib.optionalString (hypervisor == "crosvm") "--tag=${tag}"} \
    ${lib.optionalString readOnly "--readonly"} \
    ${lib.escapeShellArgs extraArgs}
''
