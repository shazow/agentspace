//go:build virtie_no_balloon

package manager

import "log/slog"

func SetBalloonLogger(l *slog.Logger) {}
