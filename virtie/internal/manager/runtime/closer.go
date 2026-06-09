package runtime

import (
	"sync"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type Closer struct {
	once  sync.Once
	state *State
}

func NewCloser(state *State) *Closer {
	return &Closer{state: state}
}

func (c *Closer) Close(fn func() error) error {
	var err error
	c.once.Do(func() {
		if c.state != nil {
			c.state.Set(control.RuntimeStopping)
		}
		err = fn()
		if c.state != nil {
			c.state.Set(control.RuntimeStopped)
		}
	})
	return err
}
