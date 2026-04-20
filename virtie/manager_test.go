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
			LockPath:   "/tmp/virtie.lock",
		},
		SSH: ManifestSSH{
			Argv: []string{"/bin/ssh"},
			User: "agent",
		},
		QEMU: ManifestQEMU{
			ArgvTemplate: []string{
				"/tmp/qemu-system-x86_64",
				"-device",
				"vhost-vsock-pci,guest-cid={{VSOCK_CID}}",
			},
		},
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
	if got, want := valid.VSock.CIDRange.Start, DefaultVSockCIDStart; got != want {
		t.Fatalf("unexpected default vsock start: got %d want %d", got, want)
	}
	if got, want := valid.VSock.CIDRange.End, DefaultVSockCIDEnd; got != want {
		t.Fatalf("unexpected default vsock end: got %d want %d", got, want)
	}

	invalidUser := *valid
	invalidUser.SSH.User = ""
	if err := invalidUser.Validate(); err == nil {
		t.Fatalf("expected validation error for missing ssh user")
	}

	invalidRange := *valid
	invalidRange.VSock.CIDRange.Start = 2
	if err := invalidRange.Validate(); err == nil {
		t.Fatalf("expected validation error for invalid cid range")
	}

	invalidQEMU := *valid
	invalidQEMU.QEMU.ArgvTemplate = []string{"/tmp/qemu-system-x86_64"}
	if err := invalidQEMU.Validate(); err == nil {
		t.Fatalf("expected validation error for missing qemu vsock placeholder")
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
			},
			User: "agent",
		},
	}

	probe := buildSSHSpec(manifest, 10, []string{"true"}, false)
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

	session := buildSSHSpec(manifest, 10, []string{"bash", "-lc", "echo hi"}, true)
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
	volumeImage := filepath.Join(tmpDir, "overlay.img")
	mkfsLog := filepath.Join(tmpDir, "mkfs.log")

	binDir := filepath.Join(tmpDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	mkfsPath := filepath.Join(binDir, "mkfs.ext4")
	if err := os.WriteFile(mkfsPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > "+mkfsLog+"\n"), 0o755); err != nil {
		t.Fatalf("write fake mkfs tool: %v", err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

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
			LockPath:   lockPath,
		},
		Persistence: ManifestPersistence{
			Directories: []string{"persist"},
		},
		SSH: ManifestSSH{
			Argv: []string{"/bin/ssh", "-q"},
			User: "agent",
		},
		QEMU: ManifestQEMU{
			ArgvTemplate: []string{
				"/bin/qemu-system-x86_64",
				"-device",
				"vhost-vsock-pci,guest-cid={{VSOCK_CID}}",
			},
		},
		Volumes: []ManifestVolume{
			{
				ImagePath:  "overlay.img",
				SizeMiB:    64,
				FSType:     "ext4",
				AutoCreate: true,
			},
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

	wantStarts := []string{"virtiofsd[ro-store]", "virtiofsd[workspace]", "qemu", "ssh", "ssh", "ssh", "ssh"}
	if !reflect.DeepEqual(runner.starts, wantStarts) {
		t.Fatalf("unexpected start order: got %v want %v", runner.starts, wantStarts)
	}

	wantQEMUArgs := []string{"-device", "vhost-vsock-pci,guest-cid=3"}
	if got := runner.qemuArgs; !reflect.DeepEqual(got, wantQEMUArgs) {
		t.Fatalf("unexpected qemu args: got %v want %v", got, wantQEMUArgs)
	}

	if got := runner.qemuEnv; len(got) != 0 {
		t.Fatalf("unexpected qemu env: got %v want no extra env", got)
	}

	if got := len(runner.sshArgs); got != 4 {
		t.Fatalf("unexpected ssh attempts: got %d want 4", got)
	}
	for i, args := range runner.sshArgs {
		if !containsString(args, "agent@vsock/3") {
			t.Fatalf("ssh attempt %d missing runtime destination: %v", i, args)
		}
	}

	wantSignals := []string{"ssh", "qemu", "virtiofsd[workspace]", "virtiofsd[ro-store]"}
	if !reflect.DeepEqual(runner.signals, wantSignals) {
		t.Fatalf("unexpected stop order: got %v want %v", runner.signals, wantSignals)
	}

	if got, want := waiter.calls, 1; got != want {
		t.Fatalf("unexpected waiter calls: got %d want %d", got, want)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "persist")); err != nil {
		t.Fatalf("expected persistence directory to exist: %v", err)
	}
	if info, err := os.Stat(volumeImage); err != nil {
		t.Fatalf("expected volume image to exist: %v", err)
	} else if got, want := info.Size(), int64(64*1024*1024); got != want {
		t.Fatalf("unexpected volume size: got %d want %d", got, want)
	}
	if data, err := os.ReadFile(mkfsLog); err != nil {
		t.Fatalf("expected mkfs log: %v", err)
	} else if got, want := strings.TrimSpace(string(data)), volumeImage; got != want {
		t.Fatalf("unexpected mkfs args: got %q want %q", got, want)
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
			Argv: []string{"/bin/ssh"},
			User: "agent",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.waitForSSH(ctx, manifest, 10)
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

func TestAllocateCIDSkipsLockedIDs(t *testing.T) {
	tmpDir := t.TempDir()
	manifest := &Manifest{
		Paths: ManifestPaths{
			WorkingDir: tmpDir,
			LockPath:   filepath.Join(tmpDir, "virtie.lock"),
		},
		VSock: ManifestVSock{
			CIDRange: ManifestVSockCIDRange{
				Start: 7,
				End:   8,
			},
		},
	}

	locker := &FileLocker{}
	held, err := locker.Acquire(manifest.ResolvedVSockLockPath(7))
	if err != nil {
		t.Fatalf("acquire held cid lock: %v", err)
	}
	defer held.Release()

	manager := &Manager{Locker: locker}
	cid, lock, err := manager.allocateCID(manifest)
	if err != nil {
		t.Fatalf("allocate cid: %v", err)
	}
	defer lock.Release()

	if cid != 8 {
		t.Fatalf("unexpected cid: got %d want 8", cid)
	}
}

func TestBuildQEMUSpecSubstitutesRuntimeCID(t *testing.T) {
	manifest := &Manifest{
		Paths: ManifestPaths{
			WorkingDir: "/tmp/work",
		},
		QEMU: ManifestQEMU{
			ArgvTemplate: []string{
				"/bin/qemu-system-x86_64",
				"-device",
				"vhost-vsock-pci,guest-cid={{VSOCK_CID}}",
				"-name",
				"agent-sandbox",
			},
		},
	}

	spec := buildQEMUSpec(manifest, 42)
	wantArgs := []string{
		"-device",
		"vhost-vsock-pci,guest-cid=42",
		"-name",
		"agent-sandbox",
	}

	if spec.Path != "/bin/qemu-system-x86_64" {
		t.Fatalf("unexpected qemu path: got %q", spec.Path)
	}
	if !reflect.DeepEqual(spec.Args, wantArgs) {
		t.Fatalf("unexpected qemu args: got %v want %v", spec.Args, wantArgs)
	}
}

type fakeRunner struct {
	mu       sync.Mutex
	starts   []string
	signals  []string
	sshArgs  [][]string
	qemuArgs []string
	qemuEnv  []string
	probes   int
	cancel   context.CancelFunc
}

func (r *fakeRunner) Start(spec ProcessSpec) (Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.starts = append(r.starts, spec.Name)

	switch spec.Name {
	case "qemu":
		r.qemuArgs = append([]string(nil), spec.Args...)
		r.qemuEnv = append([]string(nil), spec.Env...)
		return &fakeProcess{name: spec.Name, runner: r, done: make(chan error, 1)}, nil
	case "ssh":
		r.sshArgs = append(r.sshArgs, append([]string(nil), spec.Args...))
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

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
