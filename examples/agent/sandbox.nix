{
  mkSandbox,
  publicKey ? "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJeBQ2MawTu2l+nJvxR4hXsivy6a1fYu46yI5mJ8QHxr agent@agent-sandbox",
  hostName ? "agentspace-example",
  homeImagePath ? null,
  protocol ? "9p",
  sshIdentityFile ? null,
  storeOverlayPath ? "/tmp/agentspace-example-store.img",
  vsockCid ? 11010,
  memoryMiB ? 1024,
  extraModules ? [ ],
  homeModules ? [ ],
}:
mkSandbox {
  connectWith = "ssh";
  hostName = hostName;
  protocol = protocol;
  sshAuthorizedKeys = [ publicKey ];
  inherit
    homeModules
    sshIdentityFile
    ;
  persistence = {
    homeImage = homeImagePath;
    storeOverlay = storeOverlayPath;
  };
  extraModules = [
    {
      microvm.mem = memoryMiB;
      microvm.vsock.cid = vsockCid;
    }
  ]
  ++ extraModules;
}
