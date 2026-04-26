# We take over a lot of the VM launching functionality with virtie, so these
# are guardrails to avoid accidentally relying on microvm.nix features which
# aren't propagated up to virtie yet.
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
        message = "Only microvm.hypervisor = \"qemu\" is supported by the dynamic virtie vsock launcher.";
      }
      {
        assertion = config.microvm.vsock.cid == null;
        message = "Static microvm.vsock.cid is unsupported; virtie allocates the vsock CID at launch time.";
      }
      {
        assertion = !config.microvm.registerWithMachined;
        message = "microvm.registerWithMachined is unsupported until dynamic vsock CID state is plumbed through machined registration.";
      }
      {
        assertion = lib.all (share: share.proto == "virtiofs") config.microvm.shares;
        message = "Only virtiofs shares are supported by the direct virtie QEMU launcher.";
      }
      {
        assertion = lib.all (interface: interface.type == "user") config.microvm.interfaces;
        message = "Only microvm.interfaces.*.type = \"user\" is supported by the direct virtie QEMU launcher.";
      }
      {
        assertion = config.microvm.graphics.enable == false;
        message = "microvm.graphics.enable is unsupported by the direct virtie QEMU launcher.";
      }
      {
        assertion = config.microvm.devices == [ ];
        message = "microvm.devices passthrough is unsupported by the direct virtie QEMU launcher.";
      }
      {
        assertion = config.microvm.storeOnDisk == false;
        message = "microvm.storeOnDisk is unsupported by the direct virtie QEMU launcher.";
      }
      {
        assertion = config.microvm.preStart == "";
        message = "microvm.preStart is unsupported by the direct virtie QEMU launcher.";
      }
      {
        assertion = config.microvm.extraArgsScript == null;
        message = "microvm.extraArgsScript is unsupported by the direct virtie QEMU launcher.";
      }
      {
        assertion = config.microvm.qemu.pcieRootPorts == [ ];
        message = "microvm.qemu.pcieRootPorts is unsupported by the direct virtie QEMU launcher.";
      }
    ];
  };
}
