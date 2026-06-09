package launch

import (
	"errors"
	"fmt"
	"os/exec"
)

type CommandError struct {
	Stage    string
	Command  string
	ExitCode int
	Err      error
}

func (e *CommandError) Error() string {
	if e.ExitCode >= 0 {
		return fmt.Sprintf("%s: %s exited with code %d: %v", e.Stage, e.Command, e.ExitCode, e.Err)
	}
	return fmt.Sprintf("%s: %s failed: %v", e.Stage, e.Command, e.Err)
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

type StageError struct {
	Stage string
	Err   error
}

func (e *StageError) Error() string {
	return fmt.Sprintf("%s: %v", e.Stage, e.Err)
}

func (e *StageError) Unwrap() error {
	return e.Err
}

func WrapStage(stage string, err error) error {
	return &StageError{Stage: stage, Err: err}
}

func WrapFixedStage(stage string) func(error) error {
	return func(err error) error {
		return WrapStage(stage, err)
	}
}

func WrapCommandError(stage string, command string, err error) error {
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &CommandError{
			Stage:    stage,
			Command:  command,
			ExitCode: exitErr.ExitCode(),
			Err:      err,
		}
	}

	return &CommandError{
		Stage:    stage,
		Command:  command,
		ExitCode: -1,
		Err:      err,
	}
}
