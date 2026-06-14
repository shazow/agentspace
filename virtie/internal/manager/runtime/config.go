package runtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type RuntimeConfig struct {
	Manifest         *manifest.Manifest
	Plan             *launch.Plan
	Paths            launch.RuntimePaths
	CID              int
	Stats            *launch.Stats
	QMP              qmpclient.Client
	SuspendRequests  *launch.SuspendCoordinator
	Processes        *launch.ProcessSet
	ShutdownDelay    time.Duration
	WaitForeground   func(context.Context, *launch.Plan) error
	WriteBack        func(context.Context) error
	Cleanup          func() error
	CloseStats       func()
	QMPTimeout       time.Duration
	Logger           *slog.Logger
	SavedSuspendExit func(error) bool
	CollectInfo      func(context.Context, string, executor.Group) (control.InfoResponse, error)
}
