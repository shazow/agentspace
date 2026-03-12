{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.agentspace.sandbox;
in
{
  options.agentspace.sandbox = {
    inbox = lib.mkOption {
      type = lib.types.listOf (
        lib.types.submodule {
          options = {
            source = lib.mkOption {
              type = lib.types.str;
              description = "Host-side path (relative to agent dir at runtime).";
            };
            mountPoint = lib.mkOption {
              type = lib.types.str;
              description = "Where to mount the share inside the VM.";
            };
          };
        }
      );
      default = [
        {
          source = "inbox/repo";
          mountPoint = "/home/${cfg.user}/mnt/inbox/repo";
        }
      ];
      description = "Read-only shares mounted into the VM.";
    };

    outbox = {
      mountPoint = lib.mkOption {
        type = lib.types.str;
        default = "/home/${cfg.user}/mnt/outbox";
        description = "Where to mount the writable outbox inside the VM.";
      };
    };

    airlock = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = "Enable inbox/outbox airlock workflow instead of directly mounting the current directory.";
      };

      workspaceMountPoint = lib.mkOption {
        type = lib.types.str;
        default = "/home/${cfg.user}/workspace";
        description = "Where to mount the current working directory when airlock mode is disabled.";
      };

      launchAgentSetup = lib.mkOption {
        type = lib.types.lines;
        default = "";
        description = "Shell snippet used by launch-agent to prepare either airlock or direct workspace mode.";
      };
    };
  };

  config = lib.mkIf cfg.enable {
    microvm.shares =
      lib.optionals cfg.airlock.enable (
        (lib.imap0 (i: inbox: {
          proto = cfg.protocol;
          tag = "inbox-${toString i}";
          source = inbox.source;
          mountPoint = inbox.mountPoint;
          readOnly = true;
        }) cfg.inbox)
        ++ [
          {
            proto = cfg.protocol;
            tag = "outbox";
            source = "outbox";
            mountPoint = cfg.outbox.mountPoint;
            securityModel = "mapped";
          }
        ]
      )
      ++ lib.optionals (!cfg.airlock.enable) [
        {
          proto = cfg.protocol;
          tag = "workspace";
          source = ".";
          mountPoint = cfg.airlock.workspaceMountPoint;
          securityModel = "mapped";
        }
      ];

    agentspace.sandbox.airlock.launchAgentSetup =
      if cfg.airlock.enable then
        ''
          AGENT_ID=''${AGENT_ID:-$(${pkgs.openssl}/bin/openssl rand -hex 3)}
          AGENT_DIR=".agentspace/agent-$AGENT_ID"

          echo "🚀 Preparing Agent Environment: $AGENT_ID"
          echo "📂 Location: $AGENT_DIR"

          mkdir -p "$AGENT_DIR/inbox" "$AGENT_DIR/outbox"
          ln -sfn "$REPO_DIR" "$AGENT_DIR/inbox/repo"

          cleanup() {
            echo "🛑 Agent shutdown."
            rm -f "$REPO_DIR/$AGENT_DIR/inbox/repo"
            rmdir "$REPO_DIR/$AGENT_DIR/inbox" 2>/dev/null || true
            if [ -z "$(ls -A "$REPO_DIR/$AGENT_DIR/outbox" 2>/dev/null)" ]; then
              echo "📭 Outbox empty, cleaning up $AGENT_DIR"
              rm -rf "$REPO_DIR/$AGENT_DIR"
            else
              echo "📬 Outbox has contents, preserving $AGENT_DIR"
            fi
          }
          trap cleanup EXIT

          cd "$AGENT_DIR"
        ''
      else
        ''
          echo "🚀 Preparing Agent Environment"
          echo "📂 Mounting current directory at ~/workspace"
          cd "$REPO_DIR"
        '';
  };
}
