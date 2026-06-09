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
