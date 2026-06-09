package launch

import (
	"context"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type notifyCall struct {
	state   string
	message string
	values  map[string]string
}

type recordingNotifier struct {
	calls []notifyCall
}

func (n *recordingNotifier) Notify(_ context.Context, state string, message string, values map[string]string) {
	n.calls = append(n.calls, notifyCall{state: state, message: message, values: values})
}

func TestNotifyRuntimeResume(t *testing.T) {
	notifier := &recordingNotifier{}
	plan := &Plan{
		Manifest: &manifest.Manifest{Identity: manifest.Identity{HostName: "agent"}},
		Notifier: notifier,
		CID:      7,
		ResumeState: &SuspendState{
			VMStatePath: "/tmp/agent.vmstate",
		},
	}
	NotifyRuntimeResume(context.Background(), plan)
	if len(notifier.calls) != 1 {
		t.Fatalf("calls: got %d want 1", len(notifier.calls))
	}
	call := notifier.calls[0]
	if call.state != NotifyStateRuntimeResume || call.message != "Restored saved VM state" {
		t.Fatalf("call: %#v", call)
	}
	if call.values["host_name"] != "agent" || call.values["vm_state_path"] != "/tmp/agent.vmstate" || call.values["cid"] != "7" {
		t.Fatalf("values: %#v", call.values)
	}
}

func TestNotifyRuntimeResumeNoopsWithoutResumeState(t *testing.T) {
	notifier := &recordingNotifier{}
	NotifyRuntimeResume(context.Background(), &Plan{Notifier: notifier})
	if len(notifier.calls) != 0 {
		t.Fatalf("calls: got %d want 0", len(notifier.calls))
	}
}
