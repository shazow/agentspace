{ lib, buildGoModule }:

buildGoModule {
  pname = "agentspace-storefs";
  version = "0.1.0";
  src = ./.;
  vendorHash = "sha256-nNm3jwyr3Zx/U883QbAjiTQk/gNhHOfaug2A6kdYH3I=";
  subPackages = [ "./cmd/agentspace-storefs" ];
  env.CGO_ENABLED = 0;
  postConfigure = ''
    # Respect the caller's debug setting instead of logging every vhost-user
    # control message unconditionally.
    substituteInPlace vendor/github.com/hanwen/go-fuse/v2/virtiofs/virtiofs.go \
      --replace-fail 'srv.Debug = true' 'srv.Debug = opts.Debug'

    # Keep the v2.10.1 transport fixes visible and fail closed if the pinned
    # source stops matching: preserve nonblocking eventfds, make queue teardown
    # lock-safe, join request handlers, and reject invalid descriptor chains
    # supplied through untrusted guest memory.
    chmod -R u+w vendor/github.com/hanwen/go-fuse/v2/internal/vhostuser
    patch --fuzz=0 -p1 -d vendor/github.com/hanwen/go-fuse/v2 \
      < ${./patches/go-fuse-vhostuser-hardening.patch}
    cp ${./patches/go-fuse-vhostuser-hardening_test.go.in} \
      vendor/github.com/hanwen/go-fuse/v2/internal/vhostuser/hardening_test.go
  '';
  preCheck = ''
    go test github.com/hanwen/go-fuse/v2/internal/vhostuser
  '';
  postInstall = ''
    install -Dm644 vendor/github.com/hanwen/go-fuse/v2/LICENSE \
      $out/share/doc/agentspace-storefs/LICENSE.go-fuse
    install -Dm644 vendor/golang.org/x/sys/LICENSE \
      $out/share/doc/agentspace-storefs/LICENSE.x-sys
  '';
  ldflags = [
    "-s"
    "-w"
  ];
  meta = {
    description = "Read-only virtio-fs backend for the Agentspace Nix store";
    license = [
      lib.licenses.mit
      lib.licenses.bsd3
    ];
    platforms = lib.platforms.linux;
    mainProgram = "agentspace-storefs";
  };
}
