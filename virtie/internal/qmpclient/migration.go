package qmpclient

import (
	"context"
	"fmt"
	"time"
)

type MigrationWait struct {
	Timeout        time.Duration
	CommandTimeout time.Duration
	PollDelay      time.Duration
}

type RestoreWait struct {
	MigrationTimeout time.Duration
	CommandTimeout   time.Duration
	PollDelay        time.Duration
}

func RestoreFromFile(ctx context.Context, client Client, path string, wait RestoreWait) error {
	if err := client.MigrateIncoming(wait.MigrationTimeout, path); err != nil {
		return err
	}
	if err := WaitForMigration(ctx, client, MigrationWait{
		Timeout:        wait.MigrationTimeout,
		CommandTimeout: wait.CommandTimeout,
		PollDelay:      wait.PollDelay,
	}); err != nil {
		return err
	}
	return client.Cont(wait.CommandTimeout)
}

func WaitForMigration(ctx context.Context, client Client, wait MigrationWait) error {
	if wait.PollDelay <= 0 {
		wait.PollDelay = time.Second
	}
	deadline := time.NewTimer(wait.Timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(wait.PollDelay)
	defer ticker.Stop()

	var lastStatus string
	for {
		status, err := client.QueryMigrate(wait.CommandTimeout)
		if err != nil {
			return err
		}
		if status != "" {
			lastStatus = status
		}
		switch status {
		case "completed":
			return nil
		case "failed", "cancelled":
			return fmt.Errorf("migration %s", status)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastStatus == "" {
				lastStatus = "unknown"
			}
			return fmt.Errorf("migration did not complete within %s; last status %q", wait.Timeout, lastStatus)
		case <-ticker.C:
		}
	}
}
