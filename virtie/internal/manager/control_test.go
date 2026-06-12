package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type fakeControlCore struct{}

func (fakeControlCore) Status(context.Context, control.StatusRequest) (control.StatusResponse, error) {
	return control.StatusResponse{State: control.RuntimeReady}, nil
}

func (fakeControlCore) Info(context.Context, control.InfoRequest) (control.InfoResponse, error) {
	return control.InfoResponse{}, nil
}

func startTestControlServerAt(t *testing.T, path string, runtime any) {
	t.Helper()
	router, err := control.NewRuntimeRouter(runtime)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create control socket directory: %v", err)
	}
	listener, err := control.Listen(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &control.Server{Handler: router}
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !strings.Contains(err.Error(), "use of closed") {
			t.Errorf("close server: %v", err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
}
