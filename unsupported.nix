# We take over a lot of the VM launching functionality with virtle, so these
# are guardrails to avoid accidentally relying on microvm.nix features which
# aren't propagated up to virtle yet.
{
  config,
  lib,
  ...
}:

{
  config = lib.mkIf config.agentspace.sandbox.enable {
    assertions = [
      {
        assertion = config.microvm.hypervisor == "qemu";
        message = "Only microvm.hypervisor = \"qemu\" is supported by the dynamic virtle vsock launcher.";
      }
      {
        assertion = config.microvm.vsock.cid == null;
        message = "Static microvm.vsock.cid is unsupported; virtle allocates the vsock CID at launch time.";
      }
      {
        assertion = !config.microvm.registerWithMachined;
        message = "microvm.registerWithMachined is unsupported until dynamic vsock CID state is plumbed through machined registration.";
      }
      {
        assertion = lib.all (
          share:
          builtins.elem share.proto [
            "virtiofs"
            "9p"
          ]
        ) config.microvm.shares;
        message = "Only virtiofs and 9p shares are supported by the direct virtle QEMU launcher.";
      }
      {
        assertion = lib.all (interface: interface.type == "user") config.microvm.interfaces;
        message = "Only microvm.interfaces.*.type = \"user\" is supported by the direct virtle QEMU launcher.";
      }
      {
        assertion = config.microvm.devices == [ ];
        message = "microvm.devices passthrough is unsupported by the direct virtle QEMU launcher.";
      }
      {
        assertion = config.microvm.preStart == "";
        message = "microvm.preStart is unsupported by the direct virtle QEMU launcher.";
      }
      {
        assertion = config.microvm.extraArgsScript == null;
        message = "microvm.extraArgsScript is unsupported by the direct virtle QEMU launcher.";
      }
      {
        assertion = config.microvm.qemu.pcieRootPorts == [ ];
        message = "microvm.qemu.pcieRootPorts is unsupported by the direct virtle QEMU launcher.";
      }
    ];
  };
}
