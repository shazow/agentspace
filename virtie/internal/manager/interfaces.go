package manager

import (
	"context"
	"io"
	"os/exec"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
)

type lock interface {
	Release() error
}

type locker interface {
	Acquire(path string) (lock, error)
}

type vsockCIDChecker interface {
	Available(cid int) (bool, error)
}

type runner interface {
	Start(name string, cmd *exec.Cmd) (executor.Process, error)
}

type socketWaiter interface {
	Wait(ctx context.Context, socketPaths []string) error
}

type sshReadyDialer interface {
	Dial(ctx context.Context, socketPath string, timeout time.Duration) (io.ReadCloser, error)
}
