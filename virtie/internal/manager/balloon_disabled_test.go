//go:build virtie_no_balloon

package manager

import "testing"

func TestBuildQEMUCommandSkipsBalloonFeatureWhenDisabled(t *testing.T) {
	manifest := validManifestWithBalloon("/tmp/work")

	spec, err := buildQEMUCommand(manifest, 42, false)
	if err != nil {
		t.Fatalf("build qemu command: %v", err)
	}
	if containsString(commandArgs(spec), "virtio-balloon-pci,id=balloon0,deflate-on-oom=off,free-page-reporting=on") {
		t.Fatalf("expected balloon qemu args to be omitted when disabled: %v", commandArgs(spec))
	}
}
