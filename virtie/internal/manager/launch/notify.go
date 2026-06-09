package launch

import (
	"context"
	"fmt"
)

const (
	NotifyStateRuntimeSuspend = "runtime:suspend"
	NotifyStateRuntimeResume  = "runtime:resume"
)

func NotifyRuntimeResume(ctx context.Context, plan *Plan) {
	if plan == nil || plan.Notifier == nil || plan.ResumeState == nil {
		return
	}
	plan.Notifier.Notify(ctx, NotifyStateRuntimeResume, "Restored saved VM state", map[string]string{
		"host_name":     plan.Manifest.Identity.HostName,
		"vm_state_path": plan.ResumeState.VMStatePath,
		"cid":           fmt.Sprintf("%d", plan.CID),
	})
}

func NotifyRuntimeSuspend(ctx context.Context, notifier NotificationSink, state SuspendState) {
	if notifier == nil {
		return
	}
	notifier.Notify(ctx, NotifyStateRuntimeSuspend, "Saved VM suspend state", map[string]string{
		"host_name":       state.HostName,
		"qmp_socket_path": state.QMPSocketPath,
		"vm_state_path":   state.VMStatePath,
		"cid":             fmt.Sprintf("%d", state.CID),
	})
}
