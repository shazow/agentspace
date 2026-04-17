{
  mkSandbox,
  pkgs,
}:
let
  testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPRGhtYDd3nA7U82/kg63i4qaCk0jgKcQv+NSTjsqvWo agent@agent-sandbox";
  hostName = "agentspace-check";
  overlayPath = "/tmp/${hostName}-store.img";
  socketPath = "/tmp/vm-${hostName}.sock";

  exampleAgent = import ../examples/agent/sandbox.nix {
    inherit mkSandbox;
    publicKey = testPublicKey;
    inherit hostName;
    homeImagePath = null;
    protocol = "9p";
    storeOverlayPath = overlayPath;
    memoryMiB = 1024;
    vsockCid = null;
    extraModules = [
      (
        { lib, ... }:
        {
          agentspace.sandbox.connectWith = lib.mkForce "console";
          microvm.vsock.cid = lib.mkForce null;
          microvm.vsock.ssh.enable = lib.mkForce false;
        }
      )
    ];
  };

  runnerPath = "${exampleAgent.config.microvm.declaredRunner.outPath}/bin/microvm-run";
in
{
  example-agent-e2e =
    pkgs.runCommandLocal "example-agent-e2e"
      {
        nativeBuildInputs = with pkgs; [
          coreutils
          expect
          gnugrep
        ];
        requiredSystemFeatures = [ "kvm" ];
        meta.timeout = 300;
      }
      ''
        set -euo pipefail

        tmpdir="$(mktemp -d)"
        workspace_dir="$tmpdir/workspace"
        console_log="$tmpdir/console.log"

        cleanup() {
          rm -f '${socketPath}' '${overlayPath}'
          rm -rf "$tmpdir"
        }
        trap cleanup EXIT INT TERM

        mkdir -p "$workspace_dir"
        printf '%s\n' 'agentspace-check' > "$workspace_dir/ci-sentinel"

        rm -f '${socketPath}' '${overlayPath}'
        cd "$workspace_dir"

        export CONSOLE_LOG="$console_log"
        ${pkgs.expect}/bin/expect <<'EOF' || true
        set timeout 180
        match_max 100000
        log_file -a $env(CONSOLE_LOG)

        spawn ${runnerPath}
        expect {
          -exact {agentspace-check login: agent (automatic login)} {}
          timeout {
            send_user "example-agent-e2e: timed out waiting for automatic login\n"
            exit 1
          }
          eof {
            send_user "example-agent-e2e: VM exited before automatic login completed\n"
            exit 1
          }
        }

        after 5000
        send -- "set -euo pipefail; test -d \"\$HOME/workspace\"; test \"\$(cat \"\$HOME/workspace/ci-sentinel\")\" = 'agentspace-check'; systemctl is-active --quiet sshd; awk '/MemTotal:/ { exit !(\$2 < 1572864) }' /proc/meminfo; echo AGENTSPACE_E2E_OK; sudo poweroff\r"
        expect {
          -re {AGENTSPACE_E2E_OK} {}
          timeout {
            send_user "example-agent-e2e: guest assertions timed out\n"
            exit 1
          }
          eof {
            send_user "example-agent-e2e: VM exited before guest assertions completed\n"
            exit 1
          }
        }
        expect eof
        catch wait _
        EOF

        if ! test -f "$console_log"; then
          echo "example-agent-e2e: console log was not written" >&2
          exit 1
        fi

        if ! tr -d '\r' < "$console_log" | grep -F 'AGENTSPACE_E2E_OK' >/dev/null; then
          echo "example-agent-e2e: success marker missing from console log" >&2
          cat "$console_log" >&2
          exit 1
        fi

        mkdir -p "$out"
        cp "$console_log" "$out/console.log"
      '';
}
