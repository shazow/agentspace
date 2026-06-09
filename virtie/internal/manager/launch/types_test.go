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
