package runtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type RuntimeConfig struct {
	Manifest        *manifest.Manifest
	Paths           launch.RuntimePaths
	CID             int
	Stats           *Stats
	QMP             qmpclient.Client
	SuspendRequests *launch.SuspendCoordinator
	Dependencies    Dependencies
}

type Dependencies struct {
	QMPTimeout       time.Duration
	Logger           *slog.Logger
	SavedSuspendExit func(error) bool
	CollectInfo      func(context.Context, string, executor.Group) (GuestInfo, error)
	HotplugStart     HotplugStarter
	HotplugSockets   HotplugSocketWaiter
	HotplugGuest     HotplugGuest
}
