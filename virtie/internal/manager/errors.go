package manager

import (
	"context"
	"errors"
	"fmt"
)

type commandError struct {
	Stage    string
	Command  string
	ExitCode int
	Err      error
}

func (e *commandError) Error() string {
	if e.ExitCode >= 0 {
		return fmt.Sprintf("%s: %s exited with code %d: %v", e.Stage, e.Command, e.ExitCode, e.Err)
	}
	return fmt.Sprintf("%s: %s failed: %v", e.Stage, e.Command, e.Err)
}

func (e *commandError) Unwrap() error {
	return e.Err
}

type stageError struct {
	Stage string
	Err   error
}

func (e *stageError) Error() string {
	return fmt.Sprintf("%s: %v", e.Stage, e.Err)
}

func (e *stageError) Unwrap() error {
	return e.Err
}

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
