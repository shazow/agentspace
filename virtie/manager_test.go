package virtie

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
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
			WorkingDir:   "/tmp/work",
			MicroVMRun:   "/tmp/microvm-run",
			VirtioFSDRun: "/tmp/virtiofsd-run",
			LockPath:     "/tmp/virtie.lock",
		},
		SSH:      ManifestSSH{Argv: []string{"/bin/ssh", "agent@vsock/10"}},
		VirtioFS: ManifestVirtioFS{SocketPaths: []string{"sock-a"}},
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
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
			WorkingDir:   tmpDir,
			MicroVMRun:   "/bin/microvm-run",
			VirtioFSDRun: "/bin/virtiofsd-run",
			LockPath:     lockPath,
		},
		Persistence: ManifestPersistence{
			Directories: []string{"persist"},
		},
		SSH: ManifestSSH{
			Argv: []string{"/bin/ssh", "-tt", "agent@vsock/10"},
		},
		VirtioFS: ManifestVirtioFS{
			SocketPaths: []string{socketA, socketB},
		},
	}

	err := manager.Launch(cancelCtx, manifest, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	wantStarts := []string{"virtiofsd", "microvm", "ssh", "ssh", "ssh", "ssh"}
	if !reflect.DeepEqual(runner.starts, wantStarts) {
		t.Fatalf("unexpected start order: got %v want %v", runner.starts, wantStarts)
	}

	wantSignals := []string{"ssh", "microvm", "virtiofsd"}
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
	case "virtiofsd", "microvm":
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
