package launch

import (
	"errors"
	"testing"
)

func TestWrapStagePreservesStageAndCause(t *testing.T) {
	cause := errors.New("failed")
	err := WrapStage("vm startup", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("wrapped error does not preserve cause: %v", err)
	}
	var stageErr *StageError
	if !errors.As(err, &stageErr) {
		t.Fatalf("error type: got %T", err)
	}
	if stageErr.Stage != "vm startup" {
		t.Fatalf("stage: got %q want vm startup", stageErr.Stage)
	}
	if got, want := err.Error(), "vm startup: failed"; got != want {
		t.Fatalf("error string: got %q want %q", got, want)
	}
}

func TestWrapFixedStage(t *testing.T) {
	cause := errors.New("failed")
	err := WrapFixedStage("restore")(cause)
	var stageErr *StageError
	if !errors.As(err, &stageErr) {
		t.Fatalf("error type: got %T", err)
	}
	if stageErr.Stage != "restore" || !errors.Is(err, cause) {
		t.Fatalf("wrapped error: %#v", err)
	}
}

func TestWrapCommandError(t *testing.T) {
	cause := errors.New("failed")
	err := WrapCommandError("active session", "ssh", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("wrapped error does not preserve cause: %v", err)
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("error type: got %T", err)
	}
	if commandErr.Stage != "active session" || commandErr.Command != "ssh" || commandErr.ExitCode != -1 {
		t.Fatalf("command error: %#v", commandErr)
	}
	if got, want := err.Error(), "active session: ssh failed: failed"; got != want {
		t.Fatalf("error string: got %q want %q", got, want)
	}
}

func TestWrapCommandErrorNoopsWithoutError(t *testing.T) {
	if err := WrapCommandError("active session", "ssh", nil); err != nil {
		t.Fatalf("error: got %v want nil", err)
	}
}
