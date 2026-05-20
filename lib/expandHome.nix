{ lib }:
homeDir: path:
if path == null || homeDir == null then
  path
else if path == "~" then
  homeDir
else if homeDir != "" && lib.hasPrefix "~/" path then
  "${homeDir}/${lib.removePrefix "~/" path}"
else
  path
