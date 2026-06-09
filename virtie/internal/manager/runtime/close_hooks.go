package runtime

import (
	"context"
	"errors"
	"io"
)

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
			if actions.WriteBackState == nil || !actions.WriteBackState.Enabled() || actions.WriteBack == nil {
				return nil
			}
			return actions.WriteBack(ctx)
		},
		Cleanup: actions.Cleanup,
		Stats:   actions.Stats,
	}
}

type CloseHookConfig struct {
	WriteBackState *WriteBackState
	WriteBack      func(context.Context) error
	Cleanup        []func() error
	Stats          *Stats
	StatsOutput    io.Writer
}

func ConfiguredCloseHooks(config CloseHookConfig) CloseHooks {
	return NewCloseHooks(CloseHookActions{
		WriteBackState: config.WriteBackState,
		WriteBack:      config.WriteBack,
		Cleanup:        JoinedCleanup(config.Cleanup...),
		Stats:          StatsFinalizer(config.Stats, config.StatsOutput),
	})
}

func JoinedCleanup(cleanup ...func() error) func() error {
	return func() error {
		var err error
		for _, cleanup := range cleanup {
			if cleanup != nil {
				err = errors.Join(err, cleanup())
			}
		}
		return err
	}
}
