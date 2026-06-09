package launch

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type RuntimeSuspendSave struct {
	Manifest      *manifest.Manifest
	QMPSocketPath string
	CID           int
	Notifier      NotificationSink
	Save          func(ctx context.Context, vmStatePath string) error
	Wrap          func(error) error
}

func SaveRuntimeSuspend(ctx context.Context, save RuntimeSuspendSave) error {
	if save.Manifest == nil {
		return wrapSuspendSaveError(save, fmt.Errorf("suspend manifest is not configured"))
	}
	if save.Save == nil {
		return wrapSuspendSaveError(save, fmt.Errorf("suspend save callback is not configured"))
	}

	statePath := VMStatePath(save.Manifest)
	if err := EnsureParentDirectories([]string{statePath}); err != nil {
		return wrapSuspendSaveError(save, err)
	}
	if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return wrapSuspendSaveError(save, fmt.Errorf("remove stale vm state %q: %w", statePath, err))
	}
	if err := save.Save(ctx, statePath); err != nil {
		return wrapSuspendSaveError(save, err)
	}

	state := SuspendState{
		HostName:      save.Manifest.Identity.HostName,
		QMPSocketPath: save.QMPSocketPath,
		VMStatePath:   statePath,
		CID:           save.CID,
		Status:        "saved",
	}
	if err := WriteSuspendStateData(save.Manifest, state); err != nil {
		return wrapSuspendSaveError(save, err)
	}
	NotifyRuntimeSuspend(ctx, save.Notifier, state)
	return nil
}

func wrapSuspendSaveError(save RuntimeSuspendSave, err error) error {
	if save.Wrap != nil {
		return save.Wrap(err)
	}
	return err
}
