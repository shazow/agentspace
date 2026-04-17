{
  mkSandbox,
  pkgs,
}:
let
  mkLaunchScript = import ./launch-script.nix { inherit pkgs; };

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
