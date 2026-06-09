package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type Stats struct {
	started         time.Time
	bootStarted     time.Time
	qmpReady        time.Time
	guestAgentReady time.Time
	filesReady      time.Time
	sshReady        time.Time
	firstSSHAttempt time.Time
	sshStarted      time.Time
	completed       time.Time
	sshAttempts     int
}

func NewStats(started time.Time) *Stats {
	return &Stats{started: started}
}

func (s *Stats) MarkBootStarted(t time.Time) {
	s.bootStarted = t
}

func (s *Stats) MarkQMPReady(t time.Time) {
	s.qmpReady = t
}

func (s *Stats) MarkGuestAgentReady(t time.Time) {
	s.guestAgentReady = t
}

func (s *Stats) MarkFilesReady(t time.Time) {
	s.filesReady = t
}

func (s *Stats) MarkSSHAttempt(t time.Time) {
	s.sshAttempts++
	if s.firstSSHAttempt.IsZero() {
		s.firstSSHAttempt = t
	}
}

func (s *Stats) MarkSSHReady(t time.Time) {
	s.sshReady = t
}

func (s *Stats) MarkSSHStarted(t time.Time) {
	s.sshStarted = t
}

func (s *Stats) MarkCompleted(t time.Time) {
	s.completed = t
}

func (s *Stats) String() string {
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
	sshReady := s.sshReady
	if sshReady.IsZero() {
		sshReady = s.sshStarted
	}
	if !s.filesReady.IsZero() && !sshReady.IsZero() {
		fields = append(fields, formatStatDuration("files_to_ssh", sshReady.Sub(s.filesReady)))
	}
	if !s.bootStarted.IsZero() && !sshReady.IsZero() {
		fields = append(fields, formatStatDuration("boot_to_ssh", sshReady.Sub(s.bootStarted)))
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

func ControlStats(stats *Stats) control.RuntimeStats {
	if stats == nil {
		return control.RuntimeStats{}
	}
	resp := control.RuntimeStats{
		StartedAt:     stats.started,
		BootStartedAt: stats.bootStarted,
		QMPReadyAt:    stats.qmpReady,
		FilesReadyAt:  stats.filesReady,
		SSHReadyAt:    stats.sshReady,
		SSHStartedAt:  stats.sshStarted,
		CompletedAt:   stats.completed,
		SSHAttempts:   stats.sshAttempts,
	}
	if !stats.started.IsZero() && !stats.bootStarted.IsZero() {
		resp.StartedToBoot = stats.bootStarted.Sub(stats.started).String()
	}
	if !stats.bootStarted.IsZero() && !stats.qmpReady.IsZero() {
		resp.BootToQMP = stats.qmpReady.Sub(stats.bootStarted).String()
	}
	sshReady := stats.sshReady
	if sshReady.IsZero() {
		sshReady = stats.sshStarted
	}
	if !stats.filesReady.IsZero() && !sshReady.IsZero() {
		resp.FilesToSSH = sshReady.Sub(stats.filesReady).String()
	}
	if !stats.bootStarted.IsZero() && !stats.completed.IsZero() {
		resp.BootToCompleted = stats.completed.Sub(stats.bootStarted).String()
	}
	if !stats.started.IsZero() && !stats.completed.IsZero() {
		resp.Total = stats.completed.Sub(stats.started).String()
	}
	return resp
}

func formatStatDuration(name string, duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	return fmt.Sprintf("%s=%s", name, duration)
}
