package launch

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/executor/executortest"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestRunSSHSessionRetriesTransientFailure(t *testing.T) {
	launchManifest := testSSHSessionManifest()
	processes := &fakeSSHSessionProcesses{}
	stats := &fakeSSHSessionStats{}
	runner := &fakeSSHSessionRunner{
		errs: []error{errors.New("Connection refused"), nil},
	}

	err := RunSSHSession(context.Background(), SSHSession{
		Plan:      &Plan{Manifest: launchManifest, CID: 10},
		Runner:    runner,
		Processes: processes,
		Stats:     stats,
		Wait: func(ctx context.Context, process *executor.Process, watchers executor.Group) error {
			return process.Wait()
		},
	})
	if err != nil {
		t.Fatalf("run ssh session: %v", err)
	}
	if got, want := len(runner.commands), 2; got != want {
		t.Fatalf("ssh starts: got %d want %d", got, want)
	}
	if got, want := processes.removed, 1; got != want {
		t.Fatalf("removed sessions: got %d want %d", got, want)
	}
	if got, want := stats.attempts, 2; got != want {
		t.Fatalf("ssh attempts: got %d want %d", got, want)
	}
	if got, want := stats.started, 2; got != want {
		t.Fatalf("ssh started: got %d want %d", got, want)
	}
}

func TestRunSSHSessionAutoprovisionsAfterAuthenticationFailure(t *testing.T) {
	launchManifest := testSSHSessionManifest()
	launchManifest.SSH.Autoprovision = true
	processes := &fakeSSHSessionProcesses{}
	runner := &fakeSSHSessionRunner{
		errs: []error{errors.New("Permission denied (publickey)."), nil},
	}
	var ensured bool
	var installed bool

	err := RunSSHSession(context.Background(), SSHSession{
		Plan:      &Plan{Manifest: launchManifest, CID: 10},
		Runner:    runner,
		Processes: processes,
		Wait: func(ctx context.Context, process *executor.Process, watchers executor.Group) error {
			return process.Wait()
		},
		EnsureKey: func(*manifest.Manifest) (SSHAutoprovisionKey, error) {
			ensured = true
			return SSHAutoprovisionKey{IdentityFile: "/tmp/id", PublicKeyFile: "/tmp/id.pub"}, nil
		},
		InstallKey: func(context.Context, *manifest.Manifest, SSHAutoprovisionKey, executor.Group) error {
			installed = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run ssh session: %v", err)
	}
	if !ensured || !installed {
		t.Fatalf("expected autoprovision hooks to run: ensured=%v installed=%v", ensured, installed)
	}
	if got, want := len(runner.commands), 2; got != want {
		t.Fatalf("ssh starts: got %d want %d", got, want)
	}
	secondArgs := runner.commands[1].Args
	if !containsString(secondArgs, "-i") || !containsString(secondArgs, "/tmp/id") || !containsString(secondArgs, "IdentitiesOnly=yes") {
		t.Fatalf("expected identity args in retry command, got %#v", secondArgs)
	}
}

func TestRunSSHSessionWrapsCommandBuildError(t *testing.T) {
	launchManifest := testSSHSessionManifest()
	launchManifest.SSH.Argv = nil
	wrappedErr := errors.New("wrapped")

	err := RunSSHSession(context.Background(), SSHSession{
		Plan:      &Plan{Manifest: launchManifest, CID: 10},
		Runner:    &fakeSSHSessionRunner{},
		Processes: &fakeSSHSessionProcesses{},
		WrapStage: func(stage string, err error) error {
			if stage != "active session" {
				t.Fatalf("stage: got %q want active session", stage)
			}
			return wrappedErr
		},
	})
	if !errors.Is(err, wrappedErr) {
		t.Fatalf("wrapped err: got %v want %v", err, wrappedErr)
	}
}

func TestRunSSHSessionDefaultsToStageWrapping(t *testing.T) {
	launchManifest := testSSHSessionManifest()
	launchManifest.SSH.Argv = nil

	err := RunSSHSession(context.Background(), SSHSession{
		Plan:      &Plan{Manifest: launchManifest, CID: 10},
		Runner:    &fakeSSHSessionRunner{},
		Processes: &fakeSSHSessionProcesses{},
	})
	var stageErr *StageError
	if !errors.As(err, &stageErr) || stageErr.Stage != "active session" {
		t.Fatalf("default wrapped err: got %v", err)
	}
}

func testSSHSessionManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Paths: manifest.Paths{WorkingDir: "/tmp/work"},
		SSH: manifest.SSH{
			Argv: []string{"/bin/ssh"},
			User: "agent",
		},
	}
}

type fakeSSHSessionRunner struct {
	commands []*exec.Cmd
	errs     []error
}

func (r *fakeSSHSessionRunner) Start(cmd *exec.Cmd) (*executor.Process, error) {
	r.commands = append(r.commands, cmd)
	var err error
	if len(r.errs) > 0 {
		err = r.errs[0]
		r.errs = r.errs[1:]
	}
	process := &executortest.Process{OverrideName: cmd.Path, Exited: true, WaitErr: err}
	return process.Process(), nil
}

type fakeSSHSessionProcesses struct {
	group   executor.Group
	removed int
}

func (p *fakeSSHSessionProcesses) Add(processes ...*executor.Process) {
	p.group.Add(processes...)
}

func (p *fakeSSHSessionProcesses) Remove(process *executor.Process) bool {
	p.removed++
	return p.group.Remove(process)
}

func (p *fakeSSHSessionProcesses) Watchers() executor.Group {
	return p.group.Snapshot()
}

type fakeSSHSessionStats struct {
	attempts int
	started  int
	times    []time.Time
}

func (s *fakeSSHSessionStats) MarkSSHAttempt(t time.Time) {
	s.attempts++
	s.times = append(s.times, t)
}

func (s *fakeSSHSessionStats) MarkSSHStarted(t time.Time) {
	s.started++
	s.times = append(s.times, t)
}
