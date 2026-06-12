package runtime

import (
	"errors"
)

type StartedRuntime interface {
	Close() error
	MarkSavedSuspend()
}

type StartupFailureActions struct {
	ShutdownResources
	LockCleanup   func() error
	SocketCleanup func() error
}

func CleanupStartError(cause error, started StartedRuntime, startup StartupFailureActions, savedSuspendExit func(error) bool) error {
	if cause == nil {
		return nil
	}
	if started != nil {
		if savedSuspendExit != nil && savedSuspendExit(cause) {
			started.MarkSavedSuspend()
		}
		return errors.Join(cause, started.Close())
	}
	return errors.Join(cause, startup.Run())
}

func (a StartupFailureActions) Run() error {
	var err error
	if a.Processes != nil {
		err = errors.Join(err, a.Processes.Close(a.ShutdownDelay))
	}
	if a.LockCleanup != nil {
		err = errors.Join(err, a.LockCleanup())
	}
	if a.QMP != nil {
		err = errors.Join(err, a.QMP.Disconnect())
	}
	if a.SocketCleanup != nil {
		err = errors.Join(err, a.SocketCleanup())
	}
	if a.Stats != nil {
		a.Stats()
	}
	return err
}
