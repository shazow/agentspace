package manager

import (
	"context"
	"errors"

	"github.com/shazow/agentspace/virtie/internal/manager/launch"
)

type commandError = launch.CommandError
type stageError = launch.StageError

// ExitCode maps launcher failures onto process exit codes.
func ExitCode(err error) int {
	var cmdErr *commandError
	if errors.As(err, &cmdErr) && cmdErr.ExitCode >= 0 {
		return cmdErr.ExitCode
	}

	if errors.Is(err, context.Canceled) {
		return 130
	}

	return 1
}
