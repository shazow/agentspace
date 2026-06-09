package launch

import (
	"context"
	"fmt"
)

type RuntimeActivation struct {
	Lifecycle *Lifecycle

	MarkReady       func()
	Configure       func()
	StartControl    func(context.Context) error
	WrapControl     func(error) error
	HandleSuspend   func(context.Context, *SuspendCoordinator) error
	Provision       GuestProvision
	EnableWriteBack func()
}

func ActivateRuntime(ctx context.Context, activation RuntimeActivation) error {
	if activation.MarkReady != nil {
		activation.MarkReady()
	}
	if activation.Configure != nil {
		activation.Configure()
	}
	if activation.StartControl != nil {
		if err := activation.StartControl(ctx); err != nil {
			if activation.WrapControl != nil {
				return activation.WrapControl(err)
			}
			return fmt.Errorf("start control: %w", err)
		}
	}
	if activation.Lifecycle != nil && activation.HandleSuspend != nil {
		if err := HandleQueuedSuspend(ctx, activation.Lifecycle, activation.HandleSuspend); err != nil {
			return err
		}
	}

	if activation.Provision.Plan != nil {
		writeBack, err := ProvisionGuest(ctx, activation.Provision)
		if err != nil {
			return err
		}
		if writeBack && activation.EnableWriteBack != nil {
			activation.EnableWriteBack()
		}
	}
	return nil
}
