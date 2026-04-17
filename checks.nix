{
  mkSandbox,
  pkgs,
}:
let
  mkLaunchScript =
    name: nixosConfig:
    let
      vmConfig = nixosConfig.config;
      runnerPath = vmConfig.microvm.declaredRunner.outPath;
      script = pkgs.writeShellScriptBin name ''
        set -euo pipefail

        REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)
        RUNNER_PATH=${runnerPath}

        ${vmConfig.agentspace.sandbox.initExtra}

        echo "🖥️  Running Agent..."
        exec "$RUNNER_PATH/bin/microvm-run"
      '';
    in
    "${script}/bin/${name}";

  vmAirlock = mkSandbox {
    extraModules = [
      ./airlock.nix
      {
        agentspace.sandbox = {
          user = "agent";
          hostName = "agent-sandbox-airlock";
          protocol = "9p";
          airlock.enable = true;
          persistence.homeImage = "./home.img";
          bundle = [ ];
        };
      }
    ];
  };

  airlockLaunchScript = mkLaunchScript "launch-agent-airlock" vmAirlock;

  vmSshReadiness = mkSandbox {
    connectWith = "ssh";
    hostName = "agent-sandbox-ssh-ready";
    protocol = "9p";
    sshAuthorizedKeys = [
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJeBQ2MawTu2l+nJvxR4hXsivy6a1fYu46yI5mJ8QHxr agent@agent-sandbox"
    ];
    persistence = {
      homeImage = null;
      storeOverlay = "/tmp/agent-sandbox-ssh-ready-store.img";
    };
  };

  sshReadinessLaunchScript = mkLaunchScript "launch-agent-ssh-readiness" vmSshReadiness;
  sshReadinessRunner = "${vmSshReadiness.config.microvm.declaredRunner.outPath}/bin/microvm-run";
in
{
  launch-agent-airlock-init = pkgs.runCommand "launch-agent-airlock-init" { } ''
    grep -F 'AGENT_ID=' ${airlockLaunchScript}
    grep -F 'trap cleanup EXIT' ${airlockLaunchScript}
    grep -F 'cd "$AGENT_DIR"' ${airlockLaunchScript}
    grep -F 'rm -rf "$REPO_DIR/$AGENT_DIR"' ${airlockLaunchScript}

    touch $out
  '';

  launch-agent-ssh-readiness = pkgs.runCommand "launch-agent-ssh-readiness" { } ''
    grep -F 'trap cleanup EXIT INT TERM' ${sshReadinessLaunchScript}
    grep -F '⏳ Waiting for guest SSH readiness...' ${sshReadinessLaunchScript}
    grep -F '${pkgs.nmap}/bin/ncat -l --vsock --udp 2 "17999"' ${sshReadinessLaunchScript}
    grep -F 'X_SYSTEMD_UNIT_ACTIVE=agentspace-ssh-ready.target' ${sshReadinessLaunchScript}
    grep -F '✅ Guest SSH readiness received.' ${sshReadinessLaunchScript}
    grep -F 'type=11,value=io.systemd.credential:vmm.notify_socket=vsock-dgram:2:17999' ${sshReadinessRunner}

    touch $out
  '';
}
