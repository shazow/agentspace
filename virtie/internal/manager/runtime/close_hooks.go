package runtime

import "context"

type CloseHooks struct {
	WriteBack func(context.Context) error
	Cleanup   func() error
	Stats     func()
}

type CloseHookActions struct {
	WriteBackState *WriteBackState
	WriteBack      func(context.Context) error
	Cleanup        func() error
	Stats          func()
}

func NewCloseHooks(actions CloseHookActions) CloseHooks {
	return CloseHooks{
		WriteBack: func(ctx context.Context) error {
			if !actions.WriteBackState.Enabled() || actions.WriteBack == nil {
				return nil
			}
			return actions.WriteBack(ctx)
		},
		Cleanup: actions.Cleanup,
		Stats:   actions.Stats,
	}
}
