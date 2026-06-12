package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type Disconnecter interface {
	Disconnect() error
}

type ShutdownResources struct {
	Processes     *ProcessSet
	ShutdownDelay time.Duration
	QMP           Disconnecter
	Stats         func()
}

type CloseActions struct {
	ShutdownResources
	WriteBack        func(context.Context) error
	WriteBackTimeout time.Duration
	SkipWriteBack    bool
	Control          *control.Server
	Cleanup          func() error
}

type Closer struct {
	once  sync.Once
	state *State
}

func NewCloser(state *State) *Closer {
	return &Closer{state: state}
}

func (c *Closer) Close(actions CloseActions) error {
	var err error
	c.once.Do(func() {
		if c.state != nil {
			c.state.Set(control.RuntimeStopping)
		}
		err = actions.Run()
		if c.state != nil {
			c.state.Set(control.RuntimeStopped)
		}
	})
	return err
}

func (a CloseActions) Run() error {
	var err error
	if a.WriteBack != nil && !a.SkipWriteBack {
		writeBackCtx, cancelWriteBack := context.WithTimeout(context.Background(), a.WriteBackTimeout)
		err = errors.Join(err, a.WriteBack(writeBackCtx))
		cancelWriteBack()
	}
	if a.Control != nil {
		err = errors.Join(err, a.Control.Close())
	}
	if a.Processes != nil {
		err = errors.Join(err, a.Processes.Close(a.ShutdownDelay))
	}
	if a.QMP != nil {
		err = errors.Join(err, a.QMP.Disconnect())
	}
	if a.Cleanup != nil {
		err = errors.Join(err, a.Cleanup())
	}
	if a.Stats != nil {
		a.Stats()
	}
	return err
}
