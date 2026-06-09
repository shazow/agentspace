package manager

import runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"

type ProcessSet = runtimepkg.ProcessSet
type launchStats = runtimepkg.Stats

var newProcessSet = runtimepkg.NewProcessSet
var newLaunchStats = runtimepkg.NewStats
