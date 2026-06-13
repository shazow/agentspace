package hotplug

import (
	"context"
	"time"

	"github.com/shazow/agentspace/virtie/internal/hotplugtypes"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

// QMPDeviceAdapter adapts a generic QMP client to hotplug device operations.
type QMPDeviceAdapter struct {
	Client interface {
		qmpclient.RawRunner
		qmpclient.DeviceController
	}
	Timeout time.Duration
}

func (a QMPDeviceAdapter) AttachDevice(ctx context.Context, device hotplugtypes.Device, bus string) (func(context.Context), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	successful := 0
	for _, command := range attachCommands(device, bus) {
		if err := a.Client.RunRaw(a.Timeout, command); err != nil {
			a.rollbackAttach(ctx, device, successful)
			return nil, err
		}
		successful++
	}
	return func(ctx context.Context) {
		a.rollbackAttach(ctx, device, successful)
	}, nil
}

func (a QMPDeviceAdapter) DetachDevice(ctx context.Context, device hotplugtypes.Device) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deviceID := qemuDeviceID(device.ID)
	if err := a.Client.DeviceDelAndWait(a.Timeout, deviceID); err != nil {
		return err
	}
	for _, command := range detachPostDeviceDelCommands(device) {
		if err := a.Client.RunRaw(a.Timeout, command); err != nil {
			return err
		}
	}
	return nil
}

func (a QMPDeviceAdapter) rollbackAttach(ctx context.Context, device hotplugtypes.Device, successful int) {
	if ctx.Err() != nil || successful == 0 {
		return
	}
	if successful == len(attachCommands(device, "")) {
		_ = a.DetachDevice(ctx, device)
		return
	}
	for _, command := range rollbackAttachCommands(device, successful) {
		_ = a.Client.RunRaw(a.Timeout, command)
	}
}
