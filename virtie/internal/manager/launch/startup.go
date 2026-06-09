package launch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type RuntimeStartupProcessSet interface {
	AddGroup(executor.Group)
	SetQEMU(*executor.Process)
	Watchers() executor.Group
}

type RuntimeStartupStats interface {
	MarkBootStarted(time.Time)
	MarkQMPReady(time.Time)
}

type RuntimeStartup struct {
	Plan      *Plan
	Processes RuntimeStartupProcessSet
	Stats     RuntimeStartupStats
	Runner    Runner
	Logger    *slog.Logger

	StartRuns      func(cid int, manifest *manifest.Manifest) (executor.Group, error)
	WaitForSockets func(ctx context.Context, stage string, socketPaths []string, watchers executor.Group) error
	WaitForQMP     func(ctx context.Context, socketPath string, watchers executor.Group) (qmpclient.Client, error)
	WrapVMStartup  func(error) error
	Now            func() time.Time
}

type RuntimeStartupResult struct {
	QEMU *executor.Process
	QMP  qmpclient.Client
}

func StartRuntimeProcesses(ctx context.Context, startup RuntimeStartup) (RuntimeStartupResult, error) {
	if startup.Plan == nil {
		return RuntimeStartupResult{}, fmt.Errorf("launch plan is not configured")
	}
	if startup.Processes == nil {
		return RuntimeStartupResult{}, fmt.Errorf("runtime process set is not configured")
	}
	if startup.StartRuns == nil {
		return RuntimeStartupResult{}, fmt.Errorf("run starter is not configured")
	}
	if startup.WaitForQMP == nil {
		return RuntimeStartupResult{}, fmt.Errorf("qmp waiter is not configured")
	}

	runProcesses, err := startup.StartRuns(startup.Plan.CID, startup.Plan.Manifest)
	if err != nil {
		return RuntimeStartupResult{}, err
	}
	startup.Processes.AddGroup(runProcesses)

	if len(startup.Plan.VirtioFSSocketPaths) > 0 {
		if startup.WaitForSockets == nil {
			return RuntimeStartupResult{}, fmt.Errorf("virtiofs socket waiter is not configured")
		}
		if startup.Logger != nil {
			startup.Logger.Info("waiting for virtiofs sockets")
		}
		if err := startup.WaitForSockets(ctx, "virtiofs startup", startup.Plan.VirtioFSSocketPaths, startup.Processes.Watchers()); err != nil {
			return RuntimeStartupResult{}, err
		}
	}

	now := startup.Now
	if now == nil {
		now = time.Now
	}
	if startup.Stats != nil {
		startup.Stats.MarkBootStarted(now())
	}
	qemu, err := StartQEMU(startup.Runner, startup.Logger, startup.Plan)
	if err != nil {
		if startup.WrapVMStartup != nil {
			err = startup.WrapVMStartup(err)
		}
		return RuntimeStartupResult{}, err
	}
	startup.Processes.SetQEMU(qemu)

	if startup.Logger != nil {
		startup.Logger.Info("waiting for qmp readiness")
	}
	client, err := startup.WaitForQMP(ctx, startup.Plan.Paths.QMPSocket, startup.Processes.Watchers())
	if err != nil {
		return RuntimeStartupResult{}, err
	}
	return RuntimeStartupResult{QEMU: qemu, QMP: client}, nil
}

type RuntimeStartupFinalize struct {
	QEMU        *executor.Process
	QMP         qmpclient.Client
	Stats       RuntimeStartupStats
	QuitTimeout time.Duration
	Now         func() time.Time
}

func FinalizeRuntimeStartup(finalize RuntimeStartupFinalize) {
	now := finalize.Now
	if now == nil {
		now = time.Now
	}
	if finalize.Stats != nil {
		finalize.Stats.MarkQMPReady(now())
	}
	if finalize.QEMU == nil || finalize.QMP == nil {
		return
	}
	finalize.QEMU.SetShutdown(func() error {
		return finalize.QMP.Quit(finalize.QuitTimeout)
	})
}
