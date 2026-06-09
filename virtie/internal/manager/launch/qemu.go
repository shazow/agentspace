package launch

import (
	"fmt"
	"log/slog"

	"github.com/shazow/agentspace/virtie/internal/executor"
)

func StartQEMU(runner Runner, logger *slog.Logger, plan *Plan) (*executor.Process, error) {
	if runner == nil {
		return nil, fmt.Errorf("qemu runner is not configured")
	}
	if logger != nil {
		if plan.ResumeState != nil {
			logger.Info("starting qemu for restore")
		} else {
			logger.Info("starting qemu")
		}
	}
	return runner.Start(plan.QEMUCommand)
}
