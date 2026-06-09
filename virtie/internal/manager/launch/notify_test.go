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

func TestSelectNotifierPrefersConfiguredNotifier(t *testing.T) {
	configured := &recordingNotifier{}
	factoryNotifier := &recordingNotifier{}
	got := SelectNotifier(&manifest.Manifest{}, configured, func(*manifest.Manifest) NotificationSink {
		return factoryNotifier
	})
	if got != configured {
		t.Fatalf("notifier: got %#v want configured notifier", got)
	}
}

func TestSelectNotifierBuildsManifestNotifier(t *testing.T) {
	factoryNotifier := &recordingNotifier{}
	var factoryManifest *manifest.Manifest
	cfg := &manifest.Manifest{}
	got := SelectNotifier(cfg, nil, func(manifest *manifest.Manifest) NotificationSink {
		factoryManifest = manifest
		return factoryNotifier
	})
	if got != factoryNotifier {
		t.Fatalf("notifier: got %#v want factory notifier", got)
	}
	if factoryManifest != cfg {
		t.Fatalf("factory manifest: got %#v want %#v", factoryManifest, cfg)
	}
}

func TestSelectNotifierNoopsWithoutConfiguredNotifierOrFactory(t *testing.T) {
	if got := SelectNotifier(&manifest.Manifest{}, nil, nil); got != nil {
		t.Fatalf("notifier: got %#v want nil", got)
	}
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

func TestNotifyRuntimeSuspend(t *testing.T) {
	notifier := &recordingNotifier{}
	NotifyRuntimeSuspend(context.Background(), notifier, SuspendState{
		HostName:      "agent",
		QMPSocketPath: "/tmp/qmp.sock",
		VMStatePath:   "/tmp/agent.vmstate",
		CID:           7,
	})
	if len(notifier.calls) != 1 {
		t.Fatalf("calls: got %d want 1", len(notifier.calls))
	}
	call := notifier.calls[0]
	if call.state != NotifyStateRuntimeSuspend || call.message != "Saved VM suspend state" {
		t.Fatalf("call: %#v", call)
	}
	if call.values["host_name"] != "agent" ||
		call.values["qmp_socket_path"] != "/tmp/qmp.sock" ||
		call.values["vm_state_path"] != "/tmp/agent.vmstate" ||
		call.values["cid"] != "7" {
		t.Fatalf("values: %#v", call.values)
	}
}

func TestNotifyRuntimeSuspendNoopsWithoutNotifier(t *testing.T) {
	NotifyRuntimeSuspend(context.Background(), nil, SuspendState{})
}
