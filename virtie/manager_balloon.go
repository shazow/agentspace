package virtie

import (
	"context"
	"errors"

	"github.com/shazow/agentspace/virtie/balloon"
	manifestpkg "github.com/shazow/agentspace/virtie/manifest"
)

func (m *Manager) startBalloonController(ctx context.Context, manifest *manifestpkg.Manifest, qmpClient QMPClient) *managedTask {
	if manifest == nil || qmpClient == nil || manifest.QEMU.Devices.Balloon == nil || manifest.QEMU.Devices.Balloon.Controller == nil {
		return nil
	}

	logger := m.Logger
	controller := &balloon.Controller{
		Session:    newBalloonSession(qmpClient),
		Logger:     logger,
		DeviceID:   manifest.QEMU.Devices.Balloon.ID,
		Config:     *manifest.QEMU.Devices.Balloon.Controller,
		QMPTimeout: m.effectiveQMPCommandTimeout(),
	}

	return startManagedTask(ctx, func(taskCtx context.Context) error {
		err := controller.Run(taskCtx)
		if err != nil && !errors.Is(err, context.Canceled) && logger != nil {
			logger.Printf("balloon controller disabled: %v", err)
		}
		return nil
	})
}

func newBalloonSession(qmpClient QMPClient) balloon.Session {
	if session, ok := qmpClient.(balloon.Session); ok {
		return session
	}
	return balloon.NewQMPSession(qmpClient)
}
