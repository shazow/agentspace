package launch

import (
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type RunStarter struct {
	Runner        Runner
	Logger        *slog.Logger
	ShutdownDelay time.Duration
}

func StartRuns(starter RunStarter, cid int, manifest *manifest.Manifest) (executor.Group, error) {
	runs, err := manifest.ResolvedRuns(cid)
	if err != nil {
		return executor.Group{}, err
	}
	if len(runs) == 0 {
		return executor.NewGroup(), nil
	}
	if starter.Runner == nil {
		return executor.Group{}, fmt.Errorf("run starter is not configured")
	}

	started := executor.NewGroup()
	for i, run := range runs {
		if starter.Logger != nil {
			starter.Logger.Info("starting run", "index", i)
		}
		cmd := executor.Command(run.Exec[0], run.Exec[1:], run.Env)
		cmd.Dir = run.Dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		process, err := starter.Runner.Start(cmd)
		if err != nil {
			_ = started.StopAll(starter.ShutdownDelay)
			return executor.Group{}, err
		}
		started.Add(process)
	}

	return started, nil
}
