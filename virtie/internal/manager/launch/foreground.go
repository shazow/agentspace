package launch

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/shazow/agentspace/virtie/internal/executor"
)

type ForegroundRuntime interface {
	SetWatchers(executor.Group)
}

type ForegroundProcesses interface {
	QEMU() *executor.Process
	VMWatchers() executor.Group
}

type ForegroundWait struct {
	Plan      *Plan
	Runtime   ForegroundRuntime
	Processes ForegroundProcesses
	Logger    *slog.Logger
	Output    io.Writer

	RunSSH         func(context.Context) error
	WaitVM         func(context.Context, *executor.Process, executor.Group) error
	RemoveRestored func(*Plan) error
}

func WaitForeground(ctx context.Context, wait ForegroundWait) error {
	plan := wait.Plan
	if plan.Options.SSH && len(plan.Manifest.SSH.Argv) > 0 {
		if err := wait.RunSSH(ctx); err != nil {
			return err
		}
		if plan.ResumeState != nil {
			return wait.RemoveRestored(plan)
		}
		return nil
	}

	if plan.ResumeState != nil {
		if err := wait.RemoveRestored(plan); err != nil {
			return err
		}
	}

	hint, err := BuildSSHCommandHint(plan.Manifest, plan.CID)
	if err != nil {
		if wait.Logger != nil {
			wait.Logger.Info("ssh command hint template failed", "err", err)
		}
	} else if hint != "" && wait.Output != nil {
		fmt.Fprintf(wait.Output, "connect with: %s\n", hint)
	}
	vmWatchers := wait.Processes.VMWatchers()
	wait.Runtime.SetWatchers(vmWatchers)
	return wait.WaitVM(ctx, wait.Processes.QEMU(), vmWatchers)
}
