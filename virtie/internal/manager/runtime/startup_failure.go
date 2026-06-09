package runtime

import (
	"errors"
	"io"
	"time"
)

type StartupFailureActions struct {
	Processes     *ProcessSet
	ShutdownDelay time.Duration
	LockCleanup   func() error
	QMP           Disconnecter
	SocketCleanup func() error
	Stats         func()
}

type StartupFailureConfig struct {
	Processes     *ProcessSet
	ShutdownDelay time.Duration
	LockCleanup   func() error
	QMP           Disconnecter
	SocketCleanup []func() error
	Stats         *Stats
	StatsOutput   io.Writer
}

func ConfiguredStartupFailureActions(config StartupFailureConfig) StartupFailureActions {
	return StartupFailureActions{
		Processes:     config.Processes,
		ShutdownDelay: config.ShutdownDelay,
		LockCleanup:   config.LockCleanup,
		QMP:           config.QMP,
		SocketCleanup: JoinedCleanup(config.SocketCleanup...),
		Stats:         StatsFinalizer(config.Stats, config.StatsOutput),
	}
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
