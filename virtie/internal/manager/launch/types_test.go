package launch

import (
	"reflect"
	"testing"
)

func TestPlanRuntimeSocketCleanupFiles(t *testing.T) {
	plan := &Plan{
		Paths: RuntimePaths{
			QMPSocket:        "/run/qmp.sock",
			GuestAgentSocket: "/run/qga.sock",
			SSHReadySocket:   "/run/ssh-ready.sock",
			ControlSocket:    "/run/virtie.sock",
		},
		CleanupFiles: []string{"/run/virtiofs.sock"},
	}

	got := plan.RuntimeSocketCleanupFiles()
	want := []string{
		"/run/qmp.sock",
		"/run/qga.sock",
		"/run/ssh-ready.sock",
		"/run/virtie.sock",
		"/run/virtiofs.sock",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected cleanup files: got %#v want %#v", got, want)
	}
}

func TestOptionsWaitMode(t *testing.T) {
	if got := (Options{SSH: true}).WaitMode(); got != WaitSSH {
		t.Fatalf("ssh wait mode: got %q want %q", got, WaitSSH)
	}
	if got := (Options{SSH: false}).WaitMode(); got != WaitVM {
		t.Fatalf("vm wait mode: got %q want %q", got, WaitVM)
	}
}
