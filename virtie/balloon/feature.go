package balloon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	govmmQemu "github.com/kata-containers/govmm/qemu"
)

func SessionFromRaw(session RawSession) Session {
	if session == nil {
		return nil
	}
	if adapted, ok := any(session).(Session); ok {
		return adapted
	}
	return NewQMPSession(session)
}

func AppendQEMUArgs(
	args []string,
	config *govmmQemu.Config,
	resolveTransport func(string) (govmmQemu.VirtioTransport, error),
	device *Device,
) ([]string, error) {
	if device == nil {
		return args, nil
	}

	balloonTransport, err := resolveTransport(device.Transport)
	if err != nil {
		return nil, err
	}
	driver := govmmQemu.BalloonDeviceTransport[balloonTransport]
	deviceParams := []string{
		driver,
		fmt.Sprintf("id=%s", device.ID),
		fmt.Sprintf("deflate-on-oom=%s", onOff(device.DeflateOnOOM)),
		fmt.Sprintf("free-page-reporting=%s", onOff(device.FreePageReporting)),
	}

	return append(args, "-device", strings.Join(deviceParams, ",")), nil
}

func ControllerTask(logger *log.Logger, qmpTimeout time.Duration, session RawSession, device *Device) func(context.Context) error {
	if device == nil || device.Controller == nil || session == nil {
		return nil
	}

	var controllerLogger Logger
	if logger != nil {
		controllerLogger = logger
	}

	controller := &Controller{
		Session:    SessionFromRaw(session),
		Logger:     controllerLogger,
		DeviceID:   device.ID,
		Config:     *device.Controller,
		QMPTimeout: qmpTimeout,
	}

	return func(ctx context.Context) error {
		err := controller.Run(ctx)
		if err != nil && !errors.Is(err, context.Canceled) && controllerLogger != nil {
			controllerLogger.Printf("balloon controller disabled: %v", err)
		}
		return nil
	}
}

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
