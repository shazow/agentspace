package manager

import (
	"context"
	"fmt"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

// Suspend pauses the running QEMU process for the manifest through QMP.
func Suspend(ctx context.Context, manifest *manifest.Manifest) error {
	return newManager().suspend(ctx, manifest)
}

// Resume continues the running QEMU process for the manifest through QMP.
func Resume(ctx context.Context, manifest *manifest.Manifest) error {
	return newManager().resume(ctx, manifest)
}

func (m *manager) suspend(ctx context.Context, manifest *manifest.Manifest) error {
	client, qmpSocketPath, err := m.connectControlQMP(ctx, manifest)
	if err != nil {
		return err
	}
	defer client.Disconnect()

	return m.suspendConnected(manifest, qmpSocketPath, client)
}

func (m *manager) resume(ctx context.Context, manifest *manifest.Manifest) error {
	client, _, err := m.connectControlQMP(ctx, manifest)
	if err != nil {
		return err
	}
	defer client.Disconnect()

	return m.resumeConnected(manifest, client)
}

func (m *manager) connectControlQMP(ctx context.Context, manifest *manifest.Manifest) (qmpClient, string, error) {
	if err := manifest.Validate(); err != nil {
		return nil, "", err
	}

	qmpSocketPath, err := manifest.ResolvedQMPSocketPath()
	if err != nil {
		return nil, "", &stageError{Stage: "qmp control", Err: err}
	}

	dialer := m.qmpDialer
	if dialer == nil {
		dialer = &socketMonitorDialer{}
	}

	client, err := dialer.Dial(ctx, qmpSocketPath, m.effectiveQMPConnectTimeout())
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, "", &stageError{Stage: "qmp control", Err: err}
	}

	return client, qmpSocketPath, nil
}

func (m *manager) suspendConnected(manifest *manifest.Manifest, qmpSocketPath string, client qmpClient) error {
	timeout := m.effectiveQMPCommandTimeout()

	status, err := client.QueryStatus(timeout)
	if err != nil {
		return &stageError{Stage: "qmp suspend", Err: err}
	}

	switch status {
	case "paused":
		return writeSuspendState(manifest, qmpSocketPath, status)
	case "running":
		if err := client.Stop(timeout); err != nil {
			return &stageError{Stage: "qmp suspend", Err: err}
		}
		return writeSuspendState(manifest, qmpSocketPath, "paused")
	default:
		return &stageError{Stage: "qmp suspend", Err: fmt.Errorf("cannot suspend VM while QMP status is %q", status)}
	}
}

func (m *manager) resumeConnected(manifest *manifest.Manifest, client qmpClient) error {
	timeout := m.effectiveQMPCommandTimeout()

	status, err := client.QueryStatus(timeout)
	if err != nil {
		return &stageError{Stage: "qmp resume", Err: err}
	}

	switch status {
	case "paused":
		if err := client.Cont(timeout); err != nil {
			return &stageError{Stage: "qmp resume", Err: err}
		}
		return removeSuspendState(manifest)
	case "running":
		return removeSuspendState(manifest)
	default:
		return &stageError{Stage: "qmp resume", Err: fmt.Errorf("cannot resume VM while QMP status is %q", status)}
	}
}
