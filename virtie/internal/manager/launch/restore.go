package launch

import (
	"context"
	"fmt"
	"log/slog"
)

type RuntimeRestore struct {
	Plan    *Plan
	Logger  *slog.Logger
	Restore func(ctx context.Context, vmStatePath string) error
	Wrap    func(error) error
}

func RestoreRuntime(ctx context.Context, restore RuntimeRestore) error {
	if restore.Plan == nil || restore.Plan.ResumeState == nil {
		return fmt.Errorf("restore plan is not configured")
	}
	if restore.Restore == nil {
		return fmt.Errorf("runtime restore callback is not configured")
	}
	if restore.Logger != nil {
		restore.Logger.Info("restoring vm state", "path", restore.Plan.ResumeState.VMStatePath)
	}
	if err := restore.Restore(ctx, restore.Plan.ResumeState.VMStatePath); err != nil {
		if restore.Wrap != nil {
			return restore.Wrap(err)
		}
		return err
	}
	NotifyRuntimeResume(ctx, restore.Plan)
	return nil
}
