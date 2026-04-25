package manager

import (
	"context"
	"fmt"
	"os"
	"time"
)

const defaultSocketPollInterval = 100 * time.Millisecond

type pollingSocketWaiter struct {
	PollInterval time.Duration
}

func (w *pollingSocketWaiter) Wait(ctx context.Context, socketPaths []string) error {
	interval := w.PollInterval
	if interval <= 0 {
		interval = defaultSocketPollInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		missing := missingSocketPaths(socketPaths)
		if len(missing) == 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for sockets: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func missingSocketPaths(paths []string) []string {
	var missing []string
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, path)
		}
	}
	return missing
}
