package virtie

import (
	"fmt"
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
