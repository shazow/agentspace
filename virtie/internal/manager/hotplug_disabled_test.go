//go:build virtie_no_hotplug

package manager

import (
	"context"
	"strings"
	"testing"
)

func TestManagerHotplugDisabledReturnsUnsupported(t *testing.T) {
	cfg := validManifest(t.TempDir())

	err := (&manager{}).hotplug(context.Background(), cfg, "cache", HotplugOptions{})
	if err == nil || !strings.Contains(err.Error(), "hotplug support is not built") {
		t.Fatalf("unexpected hotplug disabled error: %v", err)
	}
}
