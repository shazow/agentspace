package runtime

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStartupFailureActionsRunInCleanupOrder(t *testing.T) {
	var calls []string
	actions := StartupFailureActions{
		LockCleanup: func() error {
			calls = append(calls, "lock")
			return nil
		},
		QMP: closeQMPFunc(func() error {
			calls = append(calls, "qmp")
			return nil
		}),
		SocketCleanup: func() error {
			calls = append(calls, "sockets")
			return nil
		},
		Stats: func() {
			calls = append(calls, "stats")
		},
	}

	if err := actions.Run(); err != nil {
		t.Fatalf("run startup failure actions: %v", err)
	}
	want := []string{"lock", "qmp", "sockets", "stats"}
	if len(calls) != len(want) {
		t.Fatalf("calls: got %#v want %#v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls: got %#v want %#v", calls, want)
		}
	}
}

func TestStartupFailureActionsJoinErrors(t *testing.T) {
	lockErr := errors.New("lock cleanup failed")
	qmpErr := errors.New("qmp disconnect failed")
	err := (StartupFailureActions{
		LockCleanup: func() error { return lockErr },
		QMP:         closeQMPFunc(func() error { return qmpErr }),
	}).Run()
	if !errors.Is(err, lockErr) || !errors.Is(err, qmpErr) {
		t.Fatalf("joined error: got %v want lock and qmp errors", err)
	}
}

func TestConfiguredStartupFailureActionsJoinsSocketCleanupAndFinalizesStats(t *testing.T) {
	socketErr := errors.New("socket cleanup failed")
	var output bytes.Buffer
	stats := NewStats(time.Now().Add(-time.Second))
	var socketCalls int

	actions := ConfiguredStartupFailureActions(StartupFailureConfig{
		SocketCleanup: []func() error{
			func() error {
				socketCalls++
				return socketErr
			},
			func() error {
				socketCalls++
				return nil
			},
		},
		Stats:       stats,
		StatsOutput: &output,
	})

	if err := actions.Run(); !errors.Is(err, socketErr) {
		t.Fatalf("startup failure error: got %v want %v", err, socketErr)
	}
	if socketCalls != 2 {
		t.Fatalf("socket cleanup calls: got %d want 2", socketCalls)
	}
	if ControlStats(stats).CompletedAt.IsZero() {
		t.Fatal("stats were not finalized")
	}
	if !strings.Contains(output.String(), "total=") {
		t.Fatalf("stats output missing runtime: %q", output.String())
	}
}
