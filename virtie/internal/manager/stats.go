package manager

import (
	"fmt"
	"strings"
	"time"
)

type launchStats struct {
	started     time.Time
	bootStarted time.Time
	sshStarted  time.Time
	completed   time.Time
}

func newLaunchStats(started time.Time) *launchStats {
	return &launchStats{started: started}
}

func (s *launchStats) MarkBootStarted(t time.Time) {
	s.bootStarted = t
}

func (s *launchStats) MarkSSHStarted(t time.Time) {
	s.sshStarted = t
}

func (s *launchStats) MarkCompleted(t time.Time) {
	s.completed = t
}

func (s *launchStats) String() string {
	var fields []string
	if !s.started.IsZero() && !s.bootStarted.IsZero() {
		fields = append(fields, formatStatDuration("started_to_boot", s.bootStarted.Sub(s.started)))
	}
	if !s.bootStarted.IsZero() && !s.sshStarted.IsZero() {
		fields = append(fields, formatStatDuration("boot_to_ssh", s.sshStarted.Sub(s.bootStarted)))
	}
	if !s.sshStarted.IsZero() && !s.completed.IsZero() {
		fields = append(fields, formatStatDuration("ssh_to_completed", s.completed.Sub(s.sshStarted)))
	}
	if s.sshStarted.IsZero() && !s.bootStarted.IsZero() && !s.completed.IsZero() {
		fields = append(fields, formatStatDuration("boot_to_completed", s.completed.Sub(s.bootStarted)))
	}
	if !s.started.IsZero() && !s.completed.IsZero() {
		fields = append(fields, formatStatDuration("total", s.completed.Sub(s.started)))
	}
	return strings.Join(fields, " ")
}

func formatStatDuration(name string, duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	return fmt.Sprintf("%s=%s", name, duration)
}
