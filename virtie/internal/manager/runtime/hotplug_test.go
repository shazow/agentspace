package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type fakeHotplugRuntime struct {
	attachID string
	detachID string
	err      error
}

func (r *fakeHotplugRuntime) Attach(_ context.Context, id string) error {
	r.attachID = id
	return r.err
}

func (r *fakeHotplugRuntime) Detach(_ context.Context, id string) error {
	r.detachID = id
	return r.err
}

func TestHotplugAttachesDevice(t *testing.T) {
	runtime := &fakeHotplugRuntime{}
	resp, err := Hotplug(context.Background(), runtime, control.HotplugRequest{ID: "cache"})
	if err != nil {
		t.Fatalf("hotplug attach: %v", err)
	}
	if resp.ID != "cache" || resp.Detach {
		t.Fatalf("response: %#v", resp)
	}
	if runtime.attachID != "cache" || runtime.detachID != "" {
		t.Fatalf("runtime calls: attach=%q detach=%q", runtime.attachID, runtime.detachID)
	}
}

func TestHotplugDetachesDevice(t *testing.T) {
	runtime := &fakeHotplugRuntime{}
	resp, err := Hotplug(context.Background(), runtime, control.HotplugRequest{ID: "cache", Detach: true})
	if err != nil {
		t.Fatalf("hotplug detach: %v", err)
	}
	if resp.ID != "cache" || !resp.Detach {
		t.Fatalf("response: %#v", resp)
	}
	if runtime.detachID != "cache" || runtime.attachID != "" {
		t.Fatalf("runtime calls: attach=%q detach=%q", runtime.attachID, runtime.detachID)
	}
}

func TestHotplugPropagatesRuntimeError(t *testing.T) {
	hotplugErr := errors.New("hotplug failed")
	_, err := Hotplug(context.Background(), &fakeHotplugRuntime{err: hotplugErr}, control.HotplugRequest{ID: "cache"})
	if !errors.Is(err, hotplugErr) {
		t.Fatalf("error: got %v want %v", err, hotplugErr)
	}
}
