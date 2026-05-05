package manager

import (
	"fmt"
	"strings"
	"time"
)

type launchStats struct {
	started         time.Time
	bootStarted     time.Time
	qmpReady        time.Time
	guestAgentReady time.Time
	filesReady      time.Time
	firstSSHAttempt time.Time
	sshStarted      time.Time
	completed       time.Time
	sshAttempts     int
}

func newLaunchStats(started time.Time) *launchStats {
	return &launchStats{started: started}
}

func (s *launchStats) MarkBootStarted(t time.Time) {
	s.bootStarted = t
}

func (s *launchStats) MarkQMPReady(t time.Time) {
	s.qmpReady = t
}

func (s *launchStats) MarkGuestAgentReady(t time.Time) {
	s.guestAgentReady = t
}

func (s *launchStats) MarkFilesReady(t time.Time) {
	s.filesReady = t
}

func (s *launchStats) MarkSSHAttempt(t time.Time) {
	s.sshAttempts++
	if s.firstSSHAttempt.IsZero() {
		s.firstSSHAttempt = t
	}
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
	if !s.bootStarted.IsZero() && !s.qmpReady.IsZero() {
		fields = append(fields, formatStatDuration("boot_to_qmp", s.qmpReady.Sub(s.bootStarted)))
	}
	if !s.qmpReady.IsZero() && !s.guestAgentReady.IsZero() {
		fields = append(fields, formatStatDuration("qmp_to_guest_agent", s.guestAgentReady.Sub(s.qmpReady)))
	}
	if !s.guestAgentReady.IsZero() && !s.filesReady.IsZero() {
		fields = append(fields, formatStatDuration("guest_agent_to_files", s.filesReady.Sub(s.guestAgentReady)))
	}
	if !s.filesReady.IsZero() && !s.firstSSHAttempt.IsZero() {
		fields = append(fields, formatStatDuration("files_to_first_ssh", s.firstSSHAttempt.Sub(s.filesReady)))
	}
	if !s.filesReady.IsZero() && !s.sshStarted.IsZero() {
		fields = append(fields, formatStatDuration("files_to_ssh", s.sshStarted.Sub(s.filesReady)))
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
	if s.sshAttempts > 0 {
		fields = append(fields, fmt.Sprintf("ssh_attempts=%d", s.sshAttempts))
	}
	return strings.Join(fields, " ")
}

func formatStatDuration(name string, duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	return fmt.Sprintf("%s=%s", name, duration)
}
