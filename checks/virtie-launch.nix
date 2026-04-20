{
  mkSandbox,
  mkLaunch,
  pkgs,
  ...
}:
let
  testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBIqXkHFLTDd7n09425txXfdOgJDUb7CpMAdCPVRS94z agentspace-virtie-test";

  vmVirtie = mkSandbox {
    sshAuthorizedKeys = [ testPublicKey ];
    sshIdentityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
  };

  launchScript = mkLaunch vmVirtie;
  manifestPath = vmVirtie.config.agentspace.sandbox.launch.virtieManifest;
  runner = vmVirtie.config.microvm.declaredRunner.outPath;
  virtiofsdHelper = "${runner}/bin/virtiofsd-run";
in
{
  launch-agent-virtie-contract = pkgs.runCommand "launch-agent-virtie-contract" { } ''
    grep -F 'virtie launch' ${launchScript}
    grep -F ${pkgs.lib.escapeShellArg manifestPath} ${launchScript}
    if grep -F 'systemd-run' ${launchScript} >/dev/null; then
      echo "launch-agent-virtie-contract: unexpected legacy systemd-run in virtie wrapper" >&2
      exit 1
    fi

    grep -F 'managed by virtie' ${virtiofsdHelper}
    if ${pkgs.nix}/bin/nix-store -q --references ${virtiofsdHelper} | grep -E 'supervisor|supervisord' >/dev/null; then
      echo "launch-agent-virtie-contract: unexpected supervisor dependency in virtiofsd helper stub" >&2
      exit 1
    fi

    touch $out
  '';
}
