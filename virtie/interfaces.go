package virtie

import (
	"context"
	"io"
	"os"
)

type Lock interface {
	Release() error
}

type Locker interface {
	Acquire(path string) (Lock, error)
}

type Process interface {
	Wait() error
	Signal(sig os.Signal) error
	Kill() error
	PID() int
}

type Runner interface {
	Start(spec ProcessSpec) (Process, error)
}

type SocketWaiter interface {
	Wait(ctx context.Context, socketPaths []string) error
}

type ProcessSpec struct {
	Name   string
	Path   string
	Args   []string
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}
