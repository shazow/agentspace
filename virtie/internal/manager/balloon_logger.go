//go:build !virtie_no_balloon

package manager

import (
	"log/slog"

	"github.com/shazow/agentspace/virtie/internal/balloon"
)

func SetBalloonLogger(l *slog.Logger) {
	balloon.SetLogger(l)
}
