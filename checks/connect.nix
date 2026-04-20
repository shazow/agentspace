{
  mkConnect,
  mkSandbox,
  pkgs,
  ...
}:
let
  connectScript = mkConnect (mkSandbox { });
in
{
  connect-agent-unsupported = pkgs.runCommand "connect-agent-unsupported" { } ''
    set -euo pipefail

    if ${connectScript} >"$TMPDIR/stdout" 2>"$TMPDIR/stderr"; then
      echo "connect-agent unexpectedly succeeded" >&2
      exit 1
    fi

    grep -F 'connect-agent is unsupported when virtie allocates vsock CIDs at launch time.' "$TMPDIR/stderr" >/dev/null
    grep -F 'Start a fresh session with launch-agent instead.' "$TMPDIR/stderr" >/dev/null

    touch $out
  '';
}
