//go:build !virtie_no_balloon

// Package balloon implements the internal virtio-balloon feature.
//
// It owns QEMU argument lowering for the virtio-balloon device and the
// optional runtime controller that adjusts guest memory through QMP. Balloon
// configuration types live in balloontypes so the manifest contract remains
// available when this feature package is omitted by build tags.
package balloon
