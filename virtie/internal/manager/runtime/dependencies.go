package runtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
)

type Dependencies struct {
	QMPTimeout       time.Duration
	Logger           *slog.Logger
	SavedSuspendExit func(error) bool
	CollectInfo      func(context.Context, string, executor.Group) (GuestInfo, error)
	HotplugStart     HotplugStarter
	HotplugSockets   HotplugSocketWaiter
	HotplugGuest     HotplugGuest
}
