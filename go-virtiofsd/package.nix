{ lib, buildGoModule }:

buildGoModule {
  pname = "agentspace-storefs";
  version = "0.1.0";
  src = ./.;
  vendorHash = "sha256-nNm3jwyr3Zx/U883QbAjiTQk/gNhHOfaug2A6kdYH3I=";
  subPackages = [ "./cmd/agentspace-storefs" ];
  env.CGO_ENABLED = 0;
  postConfigure = ''
    # go-fuse v2.10.1 waits for a queue reader blocked on eventfd when QEMU
    # disables a vring. Wake it after cancellation so QEMU shutdown can finish.
    substituteInPlace vendor/github.com/hanwen/go-fuse/v2/internal/vhostuser/virtq.go \
      --replace-fail \
        'close(vq.control.cancel)' \
        'close(vq.control.cancel); _, _ = syscall.Write(vq.KickFD, []byte{1, 0, 0, 0, 0, 0, 0, 0})'
    # QEMU requests GET_VRING_BASE while disconnecting. Stop the reader and
    # return its last available index as required by the vhost-user protocol.
    substituteInPlace vendor/github.com/hanwen/go-fuse/v2/internal/vhostuser/device.go \
      --replace-fail \
        $'func (d *Device) SetVringEnable(state *VhostVringState) {' \
        $'func (d *Device) GetVringBase(state *VhostVringState) {\n\tvq := d.vqs[state.Index]\n\tvq.SetEnable(nil)\n\tstate.Num = uint32(vq.LastAvailIdx)\n}\n\nfunc (d *Device) SetVringEnable(state *VhostVringState) {'
    substituteInPlace vendor/github.com/hanwen/go-fuse/v2/internal/vhostuser/server.go \
      --replace-fail \
        $'\tcase REQ_SET_VRING_ENABLE:\n\t\treq := (*VhostVringState)(inPayloadPtr)\n\t\ts.device.SetVringEnable(req)' \
        $'\tcase REQ_SET_VRING_ENABLE:\n\t\treq := (*VhostVringState)(inPayloadPtr)\n\t\ts.device.SetVringEnable(req)\n\tcase REQ_GET_VRING_BASE:\n\t\treq := (*VhostVringState)(inPayloadPtr)\n\t\ts.device.GetVringBase(req)\n\t\tr := (*VhostVringState)(outPayloadPtr)\n\t\t*r = *req\n\t\trep = r'

    # Respect the caller's debug setting instead of logging every vhost-user
    # control message unconditionally.
    substituteInPlace vendor/github.com/hanwen/go-fuse/v2/virtiofs/virtiofs.go \
      --replace-fail 'srv.Debug = true' 'srv.Debug = opts.Debug'

    # SCM_RIGHTS duplicates share file status flags. go-fuse must not clear
    # O_NONBLOCK on QEMU's kick eventfds: QEMU drains the same open file
    # descriptions during shutdown and otherwise blocks before replying to QMP
    # quit. Poll the nonblocking descriptor instead.
    chmod -R u+w vendor/github.com/hanwen/go-fuse/v2/internal/vhostuser
    patch -p1 -d vendor/github.com/hanwen/go-fuse/v2 \
      < ${./patches/go-fuse-vhostuser-nonblocking.patch}
    cp ${./patches/go-fuse-vhostuser-nonblocking_test.go.in} \
      vendor/github.com/hanwen/go-fuse/v2/internal/vhostuser/nonblocking_test.go
  '';
  preCheck = ''
    go test github.com/hanwen/go-fuse/v2/internal/vhostuser
  '';
  ldflags = [
    "-s"
    "-w"
  ];
  meta = {
    description = "Read-only virtio-fs backend for the Agentspace Nix store";
    license = lib.licenses.mit;
    platforms = lib.platforms.linux;
    mainProgram = "agentspace-storefs";
  };
}
