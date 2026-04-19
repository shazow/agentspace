package virtie

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManifestValidate(t *testing.T) {
	manifest := &Manifest{}
	if err := manifest.Validate(); err == nil {
		t.Fatalf("expected validation error for empty manifest")
	}

	valid := &Manifest{
		Identity: ManifestIdentity{HostName: "agent-sandbox"},
		Paths: ManifestPaths{
			WorkingDir: "/tmp/work",
			MicroVMRun: "/tmp/microvm-run",
			LockPath:   "/tmp/virtie.lock",
		},
		SSH: ManifestSSH{Argv: []string{"/bin/ssh", "agent@vsock/10"}},
		VirtioFS: ManifestVirtioFS{Daemons: []ManifestVirtioFSDaemon{
			{
				Tag:        "workspace",
				SocketPath: "sock-a",
				Command: ManifestCommand{
					Path: "/tmp/virtiofsd-workspace",
				},
			},
		}},
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestBuildSSHSpecPrependsModeSpecificOptions(t *testing.T) {
	manifest := &Manifest{
		Paths: ManifestPaths{
			WorkingDir: "/tmp/work",
		},
		SSH: ManifestSSH{
			Argv: []string{
				"/bin/ssh",
				"-q",
				"-o",
				"StrictHostKeyChecking=no",
				"agent@vsock/10",
			},
		},
	}

	probe := buildSSHSpec(manifest, []string{"true"}, false)
	wantProbeArgs := []string{
		"-o",
		"BatchMode=yes",
		"-o",
		"ConnectTimeout=1",
		"-q",
		"-o",
		"StrictHostKeyChecking=no",
		"agent@vsock/10",
		"true",
	}
	if !reflect.DeepEqual(probe.Args, wantProbeArgs) {
		t.Fatalf("unexpected ssh probe args: got %v want %v", probe.Args, wantProbeArgs)
	}

	session := buildSSHSpec(manifest, []string{"bash", "-lc", "echo hi"}, true)
	wantSessionArgs := []string{
		"-tt",
		"-q",
		"-o",
		"StrictHostKeyChecking=no",
		"agent@vsock/10",
		"bash",
		"-lc",
		"echo hi",
	}
	if !reflect.DeepEqual(session.Args, wantSessionArgs) {
		t.Fatalf("unexpected ssh session args: got %v want %v", session.Args, wantSessionArgs)
	}

	if session.Stdin != os.Stdin || session.Stdout != os.Stdout || session.Stderr != os.Stderr {
		t.Fatalf("expected interactive ssh session to inherit stdio")
	}
}

func TestManagerLaunchSequenceAndTeardownOrder(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "virtie.lock")
	socketA := filepath.Join(tmpDir, "sock-a")
	socketB := filepath.Join(tmpDir, "sock-b")

	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &fakeRunner{cancel: cancel}
	waiter := &fakeSocketWaiter{
		callback: func(paths []string) error {
			for _, path := range paths {
				file, err := os.Create(path)
				if err != nil {
					return err
				}
				file.Close()
			}
			return nil
		},
	}

	manager := &Manager{
		Locker:        &FileLocker{},
		Runner:        runner,
		SocketWaiter:  waiter,
		Logger:        log.New(io.Discard, "", 0),
		SSHRetryDelay: 0,
		ShutdownDelay: 10 * time.Millisecond,
	}

	manifest := &Manifest{
		Identity: ManifestIdentity{HostName: "agent-sandbox"},
		Paths: ManifestPaths{
			WorkingDir: tmpDir,
			MicroVMRun: "/bin/microvm-run",
			LockPath:   lockPath,
		},
		Persistence: ManifestPersistence{
			Directories: []string{"persist"},
		},
		SSH: ManifestSSH{
			Argv: []string{"/bin/ssh", "-tt", "agent@vsock/10"},
		},
		VirtioFS: ManifestVirtioFS{
			Daemons: []ManifestVirtioFSDaemon{
				{
					Tag:        "ro-store",
					SocketPath: socketA,
					Command: ManifestCommand{
						Path: "/bin/virtiofsd-ro-store",
					},
				},
				{
					Tag:        "workspace",
					SocketPath: socketB,
					Command: ManifestCommand{
						Path: "/bin/virtiofsd-workspace",
					},
				},
			},
		},
	}

	err := manager.Launch(cancelCtx, manifest, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	wantStarts := []string{"virtiofsd[ro-store]", "virtiofsd[workspace]", "microvm", "ssh", "ssh", "ssh", "ssh"}
	if !reflect.DeepEqual(runner.starts, wantStarts) {
		t.Fatalf("unexpected start order: got %v want %v", runner.starts, wantStarts)
	}

	wantSignals := []string{"ssh", "microvm", "virtiofsd[workspace]", "virtiofsd[ro-store]"}
	if !reflect.DeepEqual(runner.signals, wantSignals) {
		t.Fatalf("unexpected stop order: got %v want %v", runner.signals, wantSignals)
	}

	if got, want := waiter.calls, 1; got != want {
		t.Fatalf("unexpected waiter calls: got %d want %d", got, want)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "persist")); err != nil {
		t.Fatalf("expected persistence directory to exist: %v", err)
	}
}

func TestWaitForSSHAbortsInFlightProbeOnCancellation(t *testing.T) {
	runner := &blockingSSHRunner{started: make(chan *blockingSSHProcess, 1)}
	manager := &Manager{
		Runner:        runner,
		Logger:        log.New(io.Discard, "", 0),
		SSHRetryDelay: time.Second,
	}

	manifest := &Manifest{
		Paths: ManifestPaths{
			WorkingDir: t.TempDir(),
		},
		SSH: ManifestSSH{
			Argv: []string{"/bin/ssh", "agent@vsock/10"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.waitForSSH(ctx, manifest, nil)
	}()

	probe := <-runner.started
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForSSH did not return after cancellation")
	}

	if got, want := probe.killCalls(), 1; got != want {
		t.Fatalf("unexpected probe kills: got %d want %d", got, want)
	}
}

type fakeRunner struct {
	mu      sync.Mutex
	starts  []string
	signals []string
	probes  int
	cancel  context.CancelFunc
}

func (r *fakeRunner) Start(spec ProcessSpec) (Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.starts = append(r.starts, spec.Name)

	switch spec.Name {
	case "microvm":
		return &fakeProcess{name: spec.Name, runner: r, done: make(chan error, 1)}, nil
	case "ssh":
		r.probes++
		if r.probes < 3 {
			return &fakeProcess{
				name:   spec.Name,
				runner: r,
				done:   closedErrorChannel(errors.New("ssh not ready")),
			}, nil
		}
		if r.probes == 3 {
			return &fakeProcess{
				name:   spec.Name,
				runner: r,
				done:   closedErrorChannel(nil),
			}, nil
		}

		process := &fakeProcess{name: spec.Name, runner: r, done: make(chan error, 1)}
		go r.cancel()
		return process, nil
	default:
		if strings.HasPrefix(spec.Name, "virtiofsd[") {
			return &fakeProcess{name: spec.Name, runner: r, done: make(chan error, 1)}, nil
		}
		return nil, errors.New("unexpected process")
	}
}

type fakeProcess struct {
	name   string
	runner *fakeRunner
	done   chan error
}

func (p *fakeProcess) Wait() error {
	err, ok := <-p.done
	if !ok {
		return nil
	}
	return err
}

func (p *fakeProcess) Signal(sig os.Signal) error {
	p.runner.mu.Lock()
	p.runner.signals = append(p.runner.signals, p.name)
	p.runner.mu.Unlock()

	select {
	case p.done <- nil:
	default:
	}
	close(p.done)
	return nil
}

func (p *fakeProcess) Kill() error {
	return p.Signal(nil)
}

func (p *fakeProcess) PID() int {
	return 1
}

type fakeSocketWaiter struct {
	calls    int
	callback func(paths []string) error
}

func (w *fakeSocketWaiter) Wait(ctx context.Context, socketPaths []string) error {
	w.calls++
	return w.callback(socketPaths)
}

func closedErrorChannel(err error) chan error {
	ch := make(chan error, 1)
	ch <- err
	close(ch)
	return ch
}

type blockingSSHRunner struct {
	started chan *blockingSSHProcess
}

func (r *blockingSSHRunner) Start(spec ProcessSpec) (Process, error) {
	if spec.Name != "ssh" {
		return nil, errors.New("unexpected process")
	}

	process := &blockingSSHProcess{done: make(chan error, 1)}
	r.started <- process
	return process, nil
}

type blockingSSHProcess struct {
	mu        sync.Mutex
	done      chan error
	killCount int
}

func (p *blockingSSHProcess) Wait() error {
	err, ok := <-p.done
	if !ok {
		return nil
	}
	return err
}

func (p *blockingSSHProcess) Signal(sig os.Signal) error {
	return p.Kill()
}

func (p *blockingSSHProcess) Kill() error {
	p.mu.Lock()
	p.killCount++
	p.mu.Unlock()

	select {
	case p.done <- nil:
	default:
	}
	close(p.done)
	return nil
}

func (p *blockingSSHProcess) PID() int {
	return 1
}

func (p *blockingSSHProcess) killCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killCount
}
