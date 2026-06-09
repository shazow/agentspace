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
