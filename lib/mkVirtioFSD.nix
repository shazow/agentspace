{ pkgs, lib }:

let
  mkVirtioFSD =
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

      opt_inode_file_handles=()
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
    '';
in
{
  __functor = _: mkVirtioFSD;

  options =
    { options, config }:
    {
      package = lib.mkOption {
        type = options.microvm.virtiofsd.package.type;
        default = config.microvm.virtiofsd.package;
        defaultText = lib.literalExpression "config.microvm.virtiofsd.package";
        description = "Host-side virtiofsd package used for managed virtiofs shares.";
      };

      group = lib.mkOption {
        type = options.microvm.virtiofsd.group.type;
        default = config.microvm.virtiofsd.group;
        defaultText = lib.literalExpression "config.microvm.virtiofsd.group";
        description = "Group ownership for managed virtiofsd sockets.";
      };

      threadPoolSize = lib.mkOption {
        type = options.microvm.virtiofsd.threadPoolSize.type;
        default = config.microvm.virtiofsd.threadPoolSize;
        defaultText = lib.literalExpression "config.microvm.virtiofsd.threadPoolSize";
        description = "Thread pool size passed to managed virtiofsd processes.";
      };

      inodeFileHandles = lib.mkOption {
        type = options.microvm.virtiofsd.inodeFileHandles.type;
        default = config.microvm.virtiofsd.inodeFileHandles;
        defaultText = lib.literalExpression "config.microvm.virtiofsd.inodeFileHandles";
        description = ''
          File handle mode passed to managed virtiofsd processes. The managed
          default is "never" to avoid noisy host filesystem probing warnings.
          Set to null to omit the flag and use virtiofsd's own default.
        '';
      };

      extraArgs = lib.mkOption {
        type = options.microvm.virtiofsd.extraArgs.type;
        default = config.microvm.virtiofsd.extraArgs;
        defaultText = lib.literalExpression "config.microvm.virtiofsd.extraArgs";
        description = "Extra arguments passed to managed virtiofsd processes.";
      };
    };

  moduleDefaults = {
    # Some workspace-backed filesystems (like FUSE) reject chgrp/chown on the
    # virtiofsd socket path with EINVAL when --socket-group is used. virtle
    # starts QEMU and virtiofsd as the same user, so managed sockets do not need
    # a group override.
    microvm.virtiofsd.group = lib.mkDefault null;
    microvm.virtiofsd.inodeFileHandles = lib.mkDefault "never";
  };
}
