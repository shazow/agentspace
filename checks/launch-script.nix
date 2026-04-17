{ pkgs }:
name: nixosConfig:
let
  vmConfig = nixosConfig.config;
  runnerPath = vmConfig.microvm.declaredRunner.outPath;
  script = pkgs.writeShellScriptBin name ''
    set -euo pipefail

    REPO_DIR=$(${pkgs.coreutils}/bin/realpath .)
    RUNNER_PATH=${runnerPath}

    ${vmConfig.agentspace.sandbox.initExtra}

    echo "🖥️  Running Agent..."
    exec "$RUNNER_PATH/bin/microvm-run"
  '';
in
"${script}/bin/${name}"
