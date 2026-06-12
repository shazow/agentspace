package runtime

import (
	"errors"
	"testing"
)

func TestJoinedCleanupRunsAllAndJoinsErrors(t *testing.T) {
	cleanupErr := errors.New("cleanup failed")
	var cleanupCalls int

	cleanup := JoinedCleanup(
		func() error {
			cleanupCalls++
			return cleanupErr
		},
		func() error {
			cleanupCalls++
			return nil
		},
	)
	if err := cleanup(); !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanup error: got %v want %v", err, cleanupErr)
	}
	if cleanupCalls != 2 {
		t.Fatalf("cleanup calls: got %d want 2", cleanupCalls)
	}
}
