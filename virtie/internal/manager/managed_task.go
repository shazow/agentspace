package manager

import runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"

type managedTask = runtimepkg.Task
type managedTaskGroup = runtimepkg.TaskGroup
type ProcessSet = runtimepkg.ProcessSet
type runtimeCloseHooks = runtimepkg.CloseHooks

var startManagedTask = runtimepkg.StartTask
var newProcessSet = runtimepkg.NewProcessSet
