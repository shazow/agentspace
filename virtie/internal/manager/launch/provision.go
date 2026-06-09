package launch

import (
	"context"
	"time"
)

type GuestProvisionStats interface {
	MarkFilesReady(time.Time)
	MarkSSHReady(time.Time)
}

type GuestProvision struct {
	Plan  *Plan
	Stats GuestProvisionStats
	Now   func() time.Time

	WriteFiles   func(context.Context) error
	WaitSSHReady func(context.Context, string) error
}

func ProvisionGuest(ctx context.Context, provision GuestProvision) (writeBackOnExit bool, err error) {
	plan := provision.Plan
	if plan.ResumeState != nil {
		return false, nil
	}
	if provision.WriteFiles != nil {
		if err := provision.WriteFiles(ctx); err != nil {
			return false, err
		}
	}
	writeBackOnExit = true
	if provision.Stats != nil {
		provision.Stats.MarkFilesReady(guestProvisionNow(provision))
	}

	if plan.Paths.SSHReadySocket != "" && provision.WaitSSHReady != nil {
		if err := provision.WaitSSHReady(ctx, plan.Paths.SSHReadySocket); err != nil {
			return false, err
		}
	}
	if provision.Stats != nil {
		provision.Stats.MarkSSHReady(guestProvisionNow(provision))
	}
	return writeBackOnExit, nil
}

func guestProvisionNow(provision GuestProvision) time.Time {
	if provision.Now != nil {
		return provision.Now()
	}
	return time.Now()
}
