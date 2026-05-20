{ pkgs, lib }:
let
  expandHome = import ./expandHome.nix { inherit lib; };
in
{
  configFile ? null,
  identityFile ? null,
  homeDir ? null,
  extraArgs ? [ ],
}:
let
  configFile' = expandHome homeDir configFile;
  identityFile' = expandHome homeDir identityFile;
in
[
  "${pkgs.openssh}/bin/ssh"
  "-o"
  "ProxyCommand=${pkgs.systemd}/lib/systemd/systemd-ssh-proxy %h %p"
  "-o"
  "ProxyUseFdpass=yes"
  "-o"
  "CheckHostIP=no"
  "-o"
  "StrictHostKeyChecking=no"
  "-o"
  "UserKnownHostsFile=/dev/null"
  "-o"
  "GlobalKnownHostsFile=/dev/null"
]
++ lib.optionals (configFile' != null) [
  "-F"
  configFile'
]
++ lib.optionals (identityFile' != null) [
  "-i"
  identityFile'
]
++ extraArgs
