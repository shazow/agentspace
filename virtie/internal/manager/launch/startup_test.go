package launch

import (
	"reflect"
	"testing"
	"time"

	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"

	"github.com/shazow/agentspace/virtie/internal/executor/executortest"
)

func TestFinalizeRuntimeStartupMarksQMPReadyAndInstallsShutdown(t *testing.T) {
	var events []string
	stats := &recordingStartupStats{events: &events}
	qmp := &startupQMPClient{}
	readyAt := time.Unix(20, 0)
	qemu := (&executortest.Process{OverrideName: "qemu"}).Process()

	FinalizeRuntimeStartup(RuntimeStartupFinalize{
		QEMU:         qemu,
		QMP:          qmp,
		MarkQMPReady: stats.MarkQMPReady,
		QuitTimeout:  25 * time.Millisecond,
		Now: func() time.Time {
			return readyAt
		},
	})

	if stats.qmpReady != readyAt {
		t.Fatalf("qmp ready: got %s want %s", stats.qmpReady, readyAt)
	}
	if got, want := events, []string{"mark-qmp-ready"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %v want %v", got, want)
	}
	if err := qemu.Stop(time.Nanosecond); err != nil {
		t.Fatalf("stop qemu: %v", err)
	}
	if qmp.quitTimeout != 25*time.Millisecond {
		t.Fatalf("quit timeout: got %s want 25ms", qmp.quitTimeout)
	}
}

type recordingStartupStats struct {
	events   *[]string
	qmpReady time.Time
}

func (s *recordingStartupStats) MarkQMPReady(t time.Time) {
	if s.events != nil {
		*s.events = append(*s.events, "mark-qmp-ready")
	}
	s.qmpReady = t
}

type startupQMPClient struct {
	quitTimeout time.Duration
}

func (c *startupQMPClient) WithRaw(time.Duration, func(*rawQMP.Monitor) error) error { return nil }
func (c *startupQMPClient) RunRaw(time.Duration, string) error                       { return nil }
func (c *startupQMPClient) DeviceDelAndWait(time.Duration, string) error             { return nil }
func (c *startupQMPClient) Stop(time.Duration) error                                 { return nil }
func (c *startupQMPClient) Cont(time.Duration) error                                 { return nil }
func (c *startupQMPClient) QueryStatus(time.Duration) (string, error)                { return "", nil }
func (c *startupQMPClient) MigrateToFile(time.Duration, string) error                { return nil }
func (c *startupQMPClient) MigrateIncoming(time.Duration, string) error              { return nil }
func (c *startupQMPClient) QueryMigrate(time.Duration) (string, error)               { return "", nil }
func (c *startupQMPClient) Quit(timeout time.Duration) error {
	c.quitTimeout = timeout
	return nil
}
func (c *startupQMPClient) Disconnect() error { return nil }
