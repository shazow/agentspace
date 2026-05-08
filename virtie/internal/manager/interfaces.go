package manager

import (
	"context"
	"io"
	"os"
	"time"
)

type lock interface {
	Release() error
}

type locker interface {
	Acquire(path string) (lock, error)
}

type process interface {
	Wait() error
	Signal(sig os.Signal) error
	Kill() error
	PID() int
}

type runner interface {
	Start(spec processSpec) (process, error)
}

type socketWaiter interface {
	Wait(ctx context.Context, socketPaths []string) error
}

type sshReadyDialer interface {
	Dial(ctx context.Context, socketPath string, timeout time.Duration) (io.ReadCloser, error)
}

type processSpec struct {
	Name         string
	Path         string
	Args         []string
	Dir          string
	Env          []string
	ProcessGroup bool
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
}
