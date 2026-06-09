package launch

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/qga"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type Lock interface {
	Release() error
}

type Locker interface {
	Acquire(path string) (Lock, error)
}

type VSockCIDChecker interface {
	Available(cid int) (bool, error)
}

type Runner interface {
	Start(cmd *exec.Cmd) (*executor.Process, error)
}

type SocketWaiter interface {
	Wait(ctx context.Context, socketPaths []string) error
}

type SSHReadyDialer interface {
	Dial(ctx context.Context, socketPath string, timeout time.Duration) (io.ReadCloser, error)
}

type PIDSignaler interface {
	Exists(pid int) error
	Signal(pid int, sig os.Signal) error
}

type Config struct {
	Locker              Locker
	VSockCIDChecker     VSockCIDChecker
	Runner              Runner
	SocketWaiter        SocketWaiter
	QMPDialer           qmpclient.Dialer
	GuestAgentDialer    qga.Dialer
	SSHReadyDialer      SSHReadyDialer
	Logger              *slog.Logger
	LogWriter           io.Writer
	SSHRetryDelay       time.Duration
	SSHReadyTimeout     time.Duration
	ShutdownDelay       time.Duration
	QMPRetryDelay       time.Duration
	QMPConnectTimeout   time.Duration
	QMPQuitTimeout      time.Duration
	QMPMigrationTimeout time.Duration
	Signals             <-chan os.Signal
	PIDSignaler         PIDSignaler
	Notifier            NotificationSink
}

func MergeConfig(base Config, override Config) Config {
	if override.Locker != nil {
		base.Locker = override.Locker
	}
	if override.VSockCIDChecker != nil {
		base.VSockCIDChecker = override.VSockCIDChecker
	}
	if override.Runner != nil {
		base.Runner = override.Runner
	}
	if override.SocketWaiter != nil {
		base.SocketWaiter = override.SocketWaiter
	}
	if override.QMPDialer != nil {
		base.QMPDialer = override.QMPDialer
	}
	if override.GuestAgentDialer != nil {
		base.GuestAgentDialer = override.GuestAgentDialer
	}
	if override.SSHReadyDialer != nil {
		base.SSHReadyDialer = override.SSHReadyDialer
	}
	if override.Logger != nil {
		base.Logger = override.Logger
	}
	if override.LogWriter != nil {
		base.LogWriter = override.LogWriter
	}
	if override.SSHRetryDelay != 0 {
		base.SSHRetryDelay = override.SSHRetryDelay
	}
	if override.SSHReadyTimeout != 0 {
		base.SSHReadyTimeout = override.SSHReadyTimeout
	}
	if override.ShutdownDelay != 0 {
		base.ShutdownDelay = override.ShutdownDelay
	}
	if override.QMPRetryDelay != 0 {
		base.QMPRetryDelay = override.QMPRetryDelay
	}
	if override.QMPConnectTimeout != 0 {
		base.QMPConnectTimeout = override.QMPConnectTimeout
	}
	if override.QMPQuitTimeout != 0 {
		base.QMPQuitTimeout = override.QMPQuitTimeout
	}
	if override.QMPMigrationTimeout != 0 {
		base.QMPMigrationTimeout = override.QMPMigrationTimeout
	}
	if override.Signals != nil {
		base.Signals = override.Signals
	}
	if override.PIDSignaler != nil {
		base.PIDSignaler = override.PIDSignaler
	}
	if override.Notifier != nil {
		base.Notifier = override.Notifier
	}
	return base
}
