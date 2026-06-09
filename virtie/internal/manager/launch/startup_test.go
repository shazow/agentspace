package launch

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"testing"
	"time"

	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/executor/executortest"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

func TestStartRuntimeProcessesSequencesStartup(t *testing.T) {
	var events []string
	processes := &recordingStartupProcesses{events: &events, group: executor.NewGroup()}
	stats := &recordingStartupStats{events: &events}
	client := &startupQMPClient{}
	bootStarted := time.Unix(10, 0)

	result, err := StartRuntimeProcesses(context.Background(), RuntimeStartup{
		Plan: &Plan{
			Manifest:            &manifest.Manifest{},
			CID:                 7,
			QEMUCommand:         exec.Command("/bin/qemu"),
			VirtioFSSocketPaths: []string{"/tmp/virtiofs.sock"},
			Paths:               RuntimePaths{QMPSocket: "/tmp/qmp.sock"},
		},
		Processes: processes,
		Stats:     stats,
		Runner:    &qemuRunner{},
		StartRuns: func(cid int, launchManifest *manifest.Manifest) (executor.Group, error) {
			events = append(events, "start-runs")
			if cid != 7 {
				t.Fatalf("cid: got %d want 7", cid)
			}
			group := executor.NewGroup()
			group.Add((&executortest.Process{OverrideName: "run"}).Process())
			return group, nil
		},
		WaitForSockets: func(_ context.Context, stage string, socketPaths []string, watchers executor.Group) error {
			events = append(events, "wait-virtiofs")
			if stage != "virtiofs startup" {
				t.Fatalf("stage: got %q want virtiofs startup", stage)
			}
			if !reflect.DeepEqual(socketPaths, []string{"/tmp/virtiofs.sock"}) {
				t.Fatalf("socket paths: got %v", socketPaths)
			}
			if len(watchers.Processes()) != 1 {
				t.Fatalf("watchers before qemu: got %d want 1", len(watchers.Processes()))
			}
			return nil
		},
		WaitForQMP: func(_ context.Context, socketPath string, watchers executor.Group) (qmpclient.Client, error) {
			events = append(events, "wait-qmp")
			if socketPath != "/tmp/qmp.sock" {
				t.Fatalf("qmp socket: got %q want /tmp/qmp.sock", socketPath)
			}
			if len(watchers.Processes()) != 2 {
				t.Fatalf("watchers after qemu: got %d want 2", len(watchers.Processes()))
			}
			return client, nil
		},
		Now: func() time.Time {
			return bootStarted
		},
	})
	if err != nil {
		t.Fatalf("start runtime processes: %v", err)
	}
	if result.QMP != client || result.QEMU == nil || processes.qemu != result.QEMU {
		t.Fatalf("result: %#v processes qemu %#v", result, processes.qemu)
	}
	if stats.bootStarted != bootStarted {
		t.Fatalf("boot started: got %s want %s", stats.bootStarted, bootStarted)
	}
	if got, want := events, []string{"start-runs", "add-group", "wait-virtiofs", "mark-boot-started", "set-qemu", "wait-qmp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %v want %v", got, want)
	}
}

func TestStartRuntimeProcessesWrapsQEMUStartError(t *testing.T) {
	startErr := errors.New("qemu failed")
	wrappedErr := errors.New("wrapped qemu")
	_, err := StartRuntimeProcesses(context.Background(), RuntimeStartup{
		Plan:      &Plan{Manifest: &manifest.Manifest{}, QEMUCommand: exec.Command("/bin/qemu")},
		Processes: &recordingStartupProcesses{group: executor.NewGroup()},
		Runner:    &qemuRunner{err: startErr},
		StartRuns: func(int, *manifest.Manifest) (executor.Group, error) {
			return executor.NewGroup(), nil
		},
		WaitForQMP: func(context.Context, string, executor.Group) (qmpclient.Client, error) {
			t.Fatal("qmp wait should not run")
			return nil, nil
		},
		WrapVMStartup: func(err error) error {
			if !errors.Is(err, startErr) {
				t.Fatalf("start err: got %v want %v", err, startErr)
			}
			return wrappedErr
		},
	})
	if !errors.Is(err, wrappedErr) {
		t.Fatalf("wrapped err: got %v want %v", err, wrappedErr)
	}
}

func TestFinalizeRuntimeStartupMarksQMPReadyAndInstallsShutdown(t *testing.T) {
	var events []string
	stats := &recordingStartupStats{events: &events}
	qmp := &startupQMPClient{}
	readyAt := time.Unix(20, 0)
	qemu := (&executortest.Process{OverrideName: "qemu"}).Process()

	FinalizeRuntimeStartup(RuntimeStartupFinalize{
		QEMU:        qemu,
		QMP:         qmp,
		Stats:       stats,
		QuitTimeout: 25 * time.Millisecond,
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

type recordingStartupProcesses struct {
	events *[]string
	group  executor.Group
	qemu   *executor.Process
}

func (p *recordingStartupProcesses) AddGroup(group executor.Group) {
	if p.events != nil {
		*p.events = append(*p.events, "add-group")
	}
	p.group.Add(group.Processes()...)
}

func (p *recordingStartupProcesses) SetQEMU(process *executor.Process) {
	if p.events != nil {
		*p.events = append(*p.events, "set-qemu")
	}
	p.qemu = process
	p.group.Add(process)
}

func (p *recordingStartupProcesses) Watchers() executor.Group {
	return p.group.Snapshot()
}

type recordingStartupStats struct {
	events      *[]string
	bootStarted time.Time
	qmpReady    time.Time
}

func (s *recordingStartupStats) MarkBootStarted(t time.Time) {
	if s.events != nil {
		*s.events = append(*s.events, "mark-boot-started")
	}
	s.bootStarted = t
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
