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

func StartRuns(runner Runner, logger *slog.Logger, shutdownDelay time.Duration, cid int, manifest *manifest.Manifest) (executor.Group, error) {
	runs, err := manifest.ResolvedRuns(cid)
	if err != nil {
		return executor.Group{}, err
	}
	if len(runs) == 0 {
		return executor.NewGroup(), nil
	}
	if runner == nil {
		return executor.Group{}, fmt.Errorf("run starter is not configured")
	}

	started := executor.NewGroup()
	for i, run := range runs {
		if logger != nil {
			logger.Info("starting run", "index", i)
		}
		cmd := executor.Command(run.Exec[0], run.Exec[1:], run.Env)
		cmd.Dir = run.Dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		process, err := runner.Start(cmd)
		if err != nil {
			_ = started.StopAll(shutdownDelay)
			return executor.Group{}, err
		}
		started.Add(process)
	}

	return started, nil
}
