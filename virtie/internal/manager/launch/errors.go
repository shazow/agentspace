package launch

import "fmt"

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
