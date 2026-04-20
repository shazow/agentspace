{
  mkSandbox,
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

  vmVirtieBalloonDisabled = mkSandbox {
    sshAuthorizedKeys = [ testPublicKey ];
    sshIdentityFile = ".agentspace-test/id_ed25519";
    persistence.homeImage = null;
    balloon = false;
  };

  manifest = builtins.fromJSON (
    builtins.unsafeDiscardStringContext (builtins.toJSON vmVirtie.config.agentspace.sandbox.launch.virtieManifestData)
  );
  disabledBalloonManifest =
    builtins.fromJSON (
      builtins.unsafeDiscardStringContext (
        builtins.toJSON vmVirtieBalloonDisabled.config.agentspace.sandbox.launch.virtieManifestData
      )
    );

  _ =
    assert manifest.qemu.binaryPath != "";
    assert manifest.qemu.name == "agent-sandbox";
    assert builtins.length manifest.qemu.machine.options > 0;
    assert manifest.qemu.machine.type == "microvm";
    assert manifest.paths.runtimeDir == "";
    assert manifest.qemu.qmp.socketPath == "qmp.sock";
    assert manifest.qemu.devices.rng.id == "rng0";
    assert builtins.length manifest.qemu.devices.virtiofs > 0;
    assert builtins.length manifest.qemu.devices.block > 0;
    assert builtins.length manifest.qemu.devices.network > 0;
    assert manifest.qemu.devices.vsock.id == "vsock0";
    assert builtins.head manifest.ssh.argv == "${pkgs.openssh}/bin/ssh";
    assert manifest.ssh.user == "agent";
    assert builtins.elem ".agentspace-test/id_ed25519" manifest.ssh.argv;
    assert builtins.length manifest.volumes > 0;
    assert builtins.any (volume: volume.imagePath == "nix-store-overlay.img") manifest.volumes;
    assert builtins.length manifest.virtiofs.daemons > 0;
    assert builtins.all (daemon: daemon.socketPath != "" && daemon.command.path != "") manifest.virtiofs.daemons;
    assert builtins.any (daemon: daemon.tag == "workspace") manifest.virtiofs.daemons;
    assert !(manifest ? vsock);
    assert !(manifest.ssh ? destination);
    assert !(manifest.qemu ? argvTemplate);
    assert !(manifest ? microvmRun);
    assert !(manifest ? virtiofsdRun);
    assert !builtins.any (arg: builtins.match ".*@vsock/.*" arg != null) manifest.ssh.argv;
    true;

  _balloon =
    assert manifest.qemu.devices ? balloon;
    assert manifest.qemu.devices.balloon.id == "balloon0";
    assert !(manifest.qemu.devices.balloon ? controller);
    assert !(disabledBalloonManifest.qemu.devices ? balloon);
    true;
in
{
  virtie-manifest-contract = assert _; pkgs.runCommand "virtie-manifest-contract" { } "touch $out";

  virtie-manifest-balloon-contract = assert _balloon; pkgs.runCommand "virtie-manifest-balloon-contract" { } "touch $out";
}
