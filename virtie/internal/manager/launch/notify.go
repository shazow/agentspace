package launch

import (
	"context"
	"fmt"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

const (
	NotifyStateRuntimeSuspend = "runtime:suspend"
	NotifyStateRuntimeResume  = "runtime:resume"
)

type NotifierFactory func(*manifest.Manifest) NotificationSink

func SelectNotifier(manifest *manifest.Manifest, configured NotificationSink, factory NotifierFactory) NotificationSink {
	if configured != nil {
		return configured
	}
	if factory == nil {
		return nil
	}
	return factory(manifest)
}

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
