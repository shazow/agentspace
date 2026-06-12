package runtime

import (
	"context"
	"errors"
)

type CloseHooks struct {
	WriteBack func(context.Context) error
	Cleanup   func() error
	Stats     func()
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
