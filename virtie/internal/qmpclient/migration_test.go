package qmpclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestWaitForMigrationCompletes(t *testing.T) {
	client := &migrationClient{statuses: []string{"active", "completed"}}
	if err := WaitForMigration(context.Background(), client, MigrationWait{
		Timeout:        time.Second,
		CommandTimeout: 10 * time.Millisecond,
		PollDelay:      time.Millisecond,
	}); err != nil {
		t.Fatalf("wait migration: %v", err)
	}
	if got, want := client.commandTimeouts, []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("command timeouts: got %#v want %#v", got, want)
	}
}

func TestWaitForMigrationReturnsTerminalFailure(t *testing.T) {
	client := &migrationClient{statuses: []string{"active", "failed"}}
	err := WaitForMigration(context.Background(), client, MigrationWait{Timeout: time.Second, PollDelay: time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "migration failed") {
		t.Fatalf("expected failed migration error, got %v", err)
	}
}

func TestWaitForMigrationTimesOutWithLastStatus(t *testing.T) {
	client := &migrationClient{statuses: []string{"setup"}}
	err := WaitForMigration(context.Background(), client, MigrationWait{Timeout: time.Millisecond, PollDelay: time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), `last status "setup"`) {
		t.Fatalf("expected timeout with last status, got %v", err)
	}
}

func TestWaitForMigrationReturnsQueryError(t *testing.T) {
	wantErr := errors.New("query migrate failed")
	client := &migrationClient{err: wantErr}
	err := WaitForMigration(context.Background(), client, MigrationWait{Timeout: time.Second, PollDelay: time.Millisecond})
	if !errors.Is(err, wantErr) {
		t.Fatalf("query error: got %v want %v", err, wantErr)
	}
}

func TestWaitForMigrationReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := &migrationClient{afterQuery: cancel}
	err := WaitForMigration(ctx, client, MigrationWait{Timeout: time.Second, PollDelay: time.Hour})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error: got %v want %v", err, context.Canceled)
	}
}

func TestRestoreFromFileMigratesWaitsAndContinues(t *testing.T) {
	client := &migrationClient{statuses: []string{"active", "completed"}}
	if err := RestoreFromFile(context.Background(), client, "/tmp/vmstate", RestoreWait{
		MigrationTimeout: 20 * time.Millisecond,
		CommandTimeout:   10 * time.Millisecond,
		PollDelay:        time.Millisecond,
	}); err != nil {
		t.Fatalf("restore from file: %v", err)
	}
	wantCalls := []string{"migrate-incoming:/tmp/vmstate", "query-migrate", "query-migrate", "cont"}
	if len(client.calls) != len(wantCalls) {
		t.Fatalf("calls: got %#v want %#v", client.calls, wantCalls)
	}
	for i := range wantCalls {
		if client.calls[i] != wantCalls[i] {
			t.Fatalf("calls: got %#v want %#v", client.calls, wantCalls)
		}
	}
	if got, want := client.migrationTimeouts, []time.Duration{20 * time.Millisecond}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("migration timeouts: got %#v want %#v", got, want)
	}
	if got, want := client.contTimeouts, []time.Duration{10 * time.Millisecond}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("cont timeouts: got %#v want %#v", got, want)
	}
}

func TestRestoreFromFileReturnsMigrateIncomingError(t *testing.T) {
	wantErr := errors.New("restore failed")
	client := &migrationClient{migrateIncomingErr: wantErr}
	err := RestoreFromFile(context.Background(), client, "/tmp/vmstate", RestoreWait{MigrationTimeout: time.Second, PollDelay: time.Millisecond})
	if !errors.Is(err, wantErr) {
		t.Fatalf("restore error: got %v want %v", err, wantErr)
	}
}

type migrationClient struct {
	Client
	statuses           []string
	err                error
	migrateIncomingErr error
	commandTimeouts    []time.Duration
	migrationTimeouts  []time.Duration
	contTimeouts       []time.Duration
	calls              []string
	afterQuery         func()
}

func (c *migrationClient) MigrateIncoming(timeout time.Duration, path string) error {
	c.calls = append(c.calls, "migrate-incoming:"+path)
	c.migrationTimeouts = append(c.migrationTimeouts, timeout)
	return c.migrateIncomingErr
}

func (c *migrationClient) Cont(timeout time.Duration) error {
	c.calls = append(c.calls, "cont")
	c.contTimeouts = append(c.contTimeouts, timeout)
	return nil
}

func (c *migrationClient) QueryMigrate(timeout time.Duration) (string, error) {
	c.calls = append(c.calls, "query-migrate")
	c.commandTimeouts = append(c.commandTimeouts, timeout)
	if c.err != nil {
		return "", c.err
	}
	if c.afterQuery != nil {
		c.afterQuery()
	}
	if len(c.statuses) == 0 {
		return "", nil
	}
	status := c.statuses[0]
	if len(c.statuses) > 1 {
		c.statuses = c.statuses[1:]
	}
	return status, nil
}
