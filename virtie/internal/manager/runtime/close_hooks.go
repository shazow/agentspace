package runtime

import "context"

type CloseHooks struct {
	WriteBack func(context.Context) error
	Cleanup   func() error
	Stats     func()
}
