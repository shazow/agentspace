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
  manifest = vmVirtie.config.agentspace.sandbox.launch.virtieManifest;
  runner = vmVirtie.config.microvm.declaredRunner.outPath;
  virtiofsdHelper = "${runner}/bin/virtiofsd-run";
in
{
  launch-agent-virtie-contract = pkgs.runCommand "launch-agent-virtie-contract" { } ''
    grep -F 'virtie launch' ${launchScript}
    grep -F ${pkgs.lib.escapeShellArg manifest} ${launchScript}
    if grep -F 'systemd-run' ${launchScript} >/dev/null; then
      echo "launch-agent-virtie-contract: unexpected legacy systemd-run in virtie wrapper" >&2
      exit 1
    fi

    grep -F '"qemu":{"binaryPath":"' ${manifest}
    grep -F '"name":"agent-sandbox"' ${manifest}
    grep -F '"machine":{"options":[' ${manifest}
    grep -F '"type":"microvm"' ${manifest}
    grep -F '"runtimeDir":""' ${manifest}
    grep -F '"qmp":{"socketPath":"qmp.sock"}' ${manifest}
    grep -F '"rng":{"id":"rng0"' ${manifest}
    grep -F '"virtiofs":[{' ${manifest}
    grep -F '"block":[{' ${manifest}
    grep -F '"network":[{' ${manifest}
    grep -F '"vsock":{"id":"vsock0"' ${manifest}
    grep -F '"argv":["${pkgs.openssh}/bin/ssh"' ${manifest}
    grep -F '"user":"agent"' ${manifest}
    grep -F '".agentspace-test/id_ed25519"' ${manifest}
    grep -F '"volumes":[' ${manifest}
    grep -F '"imagePath":"nix-store-overlay.img"' ${manifest}
    grep -F '"daemons":[' ${manifest}
    grep -F '"socketPath":"' ${manifest}
    grep -F '"command":{' ${manifest}
    grep -F 'workspace' ${manifest}
    if grep -F '"cidRange"' ${manifest} >/dev/null; then
      echo "launch-agent-virtie-contract: manifest must not explicitly supply a vsock cid range" >&2
      exit 1
    fi
    if grep -F '@vsock/' ${manifest} >/dev/null; then
      echo "launch-agent-virtie-contract: manifest must not embed the vsock destination" >&2
      exit 1
    fi
    if grep -F '"argvTemplate"' ${manifest} >/dev/null; then
      echo "launch-agent-virtie-contract: manifest must not include a legacy qemu argv template" >&2
      exit 1
    fi
    if grep -F '"microvmRun":"' ${manifest} >/dev/null; then
      echo "launch-agent-virtie-contract: manifest must not reference microvm-run" >&2
      exit 1
    fi
    if grep -F '"virtiofsdRun":"' ${manifest} >/dev/null; then
      echo "launch-agent-virtie-contract: unexpected virtiofsd-run helper in manifest" >&2
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
