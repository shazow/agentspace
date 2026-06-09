package runtime

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStatsString(t *testing.T) {
	started := time.Unix(100, 0)
	stats := NewStats(started)
	stats.MarkBootStarted(started.Add(10 * time.Millisecond))
	stats.MarkQMPReady(started.Add(30 * time.Millisecond))
	stats.MarkGuestAgentReady(started.Add(40 * time.Millisecond))
	stats.MarkFilesReady(started.Add(60 * time.Millisecond))
	stats.MarkSSHAttempt(started.Add(80 * time.Millisecond))
	stats.MarkSSHStarted(started.Add(100 * time.Millisecond))
	stats.MarkCompleted(started.Add(150 * time.Millisecond))

	got := stats.String()
	for _, want := range []string{
		"started_to_boot=10ms",
		"boot_to_qmp=20ms",
		"qmp_to_guest_agent=10ms",
		"guest_agent_to_files=20ms",
		"files_to_first_ssh=20ms",
		"files_to_ssh=40ms",
		"ssh_to_completed=50ms",
		"total=150ms",
		"ssh_attempts=1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stats string %q missing %q", got, want)
		}
	}
}

func TestControlStats(t *testing.T) {
	started := time.Unix(100, 0)
	stats := NewStats(started)
	stats.MarkBootStarted(started.Add(time.Second))
	stats.MarkQMPReady(started.Add(2 * time.Second))
	stats.MarkFilesReady(started.Add(3 * time.Second))
	stats.MarkSSHReady(started.Add(5 * time.Second))
	stats.MarkCompleted(started.Add(8 * time.Second))
	stats.MarkSSHAttempt(started.Add(4 * time.Second))

	got := ControlStats(stats)
	if got.StartedAt != started || got.BootStartedAt != started.Add(time.Second) {
		t.Fatalf("unexpected timestamps: %#v", got)
	}
	if got.StartedToBoot != "1s" || got.BootToQMP != "1s" || got.FilesToSSH != "2s" || got.BootToCompleted != "7s" || got.Total != "8s" {
		t.Fatalf("unexpected durations: %#v", got)
	}
	if got.SSHAttempts != 1 {
		t.Fatalf("unexpected ssh attempts: got %d want 1", got.SSHAttempts)
	}
}

func TestStatsFinalizerMarksCompletedAndWritesOutput(t *testing.T) {
	stats := NewStats(time.Now().Add(-time.Second))
	stats.MarkBootStarted(time.Now().Add(-500 * time.Millisecond))
	var output bytes.Buffer
	StatsFinalizer(stats, &output)()
	got := output.String()
	if !strings.HasPrefix(got, "stats: ") || !strings.Contains(got, "total=") {
		t.Fatalf("unexpected stats output: %q", got)
	}
	if ControlStats(stats).CompletedAt.IsZero() {
		t.Fatal("stats finalizer did not mark completion")
	}
}
