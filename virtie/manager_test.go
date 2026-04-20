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

	valid := validManifest("/tmp/work")

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

	invalidQMP := *valid
	invalidQMP.QEMU.QMP.SocketPath = ""
	if err := invalidQMP.Validate(); err == nil {
		t.Fatalf("expected validation error for missing qmp socket path")
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
	manifest := validManifest(tmpDir)
	manifest.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")
	manifest.Persistence.Directories = []string{"persist"}
	manifest.QEMU.QMP.SocketPath = "qmp.sock"
	manifest.QEMU.Devices.Block[0].ImagePath = "overlay.img"
	manifest.Volumes = []ManifestVolume{
		{
			ImagePath:  "overlay.img",
			SizeMiB:    64,
			FSType:     "ext4",
			AutoCreate: true,
		},
	}
	manifest.VirtioFS.Daemons = []ManifestVirtioFSDaemon{
		{
			Tag:        "ro-store",
			SocketPath: "sock-a",
			Command: ManifestCommand{
				Path: "/bin/virtiofsd-ro-store",
			},
		},
		{
			Tag:        "workspace",
			SocketPath: "sock-b",
			Command: ManifestCommand{
				Path: "/bin/virtiofsd-workspace",
			},
		},
	}
	manifest.QEMU.Devices.VirtioFS = []ManifestQEMUVirtioFSShare{
		{ID: "fs0", SocketPath: "sock-a", Tag: "ro-store", Transport: "pci"},
		{ID: "fs1", SocketPath: "sock-b", Tag: "workspace", Transport: "pci"},
	}

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
	qmpClient := &fakeQMPClient{
		onQuit: func() {
			runner.exitQEMU(nil)
		},
	}
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
		Locker:            &FileLocker{},
		Runner:            runner,
		SocketWaiter:      waiter,
		QMPDialer:         &fakeQMPDialer{client: qmpClient},
		Logger:            log.New(io.Discard, "", 0),
		SSHRetryDelay:     0,
		ShutdownDelay:     10 * time.Millisecond,
		QMPRetryDelay:     0,
		QMPConnectTimeout: time.Millisecond,
		QMPQuitTimeout:    time.Millisecond,
	}

	err := manager.Launch(cancelCtx, manifest, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	wantStarts := []string{"virtiofsd[ro-store]", "virtiofsd[workspace]", "qemu", "ssh", "ssh", "ssh", "ssh"}
	if !reflect.DeepEqual(runner.starts, wantStarts) {
		t.Fatalf("unexpected start order: got %v want %v", runner.starts, wantStarts)
	}

	if !containsString(runner.qemuArgs, "-qmp") {
		t.Fatalf("expected qemu args to contain qmp socket: %v", runner.qemuArgs)
	}
	if !containsString(runner.qemuArgs, "unix:"+filepath.Join(tmpDir, "qmp.sock")+",server,nowait") {
		t.Fatalf("expected qemu args to contain resolved qmp socket path: %v", runner.qemuArgs)
	}
	if !containsString(runner.qemuArgs, "guest-cid=3") {
		t.Fatalf("expected qemu args to contain runtime vsock cid: %v", runner.qemuArgs)
	}
	if !containsString(runner.qemuArgs, "virtio-blk-pci,drive=vda") {
		t.Fatalf("expected qemu args to contain virtio block device: %v", runner.qemuArgs)
	}
	if !containsString(runner.qemuArgs, "vhost-user-fs-pci,chardev=char-fs1,tag=workspace") {
		t.Fatalf("expected qemu args to contain virtiofs share: %v", runner.qemuArgs)
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

	wantSignals := []string{"ssh", "virtiofsd[workspace]", "virtiofsd[ro-store]"}
	if !reflect.DeepEqual(runner.signals, wantSignals) {
		t.Fatalf("unexpected stop order: got %v want %v", runner.signals, wantSignals)
	}
	if qmpClient.quitCalls != 1 {
		t.Fatalf("expected qmp quit to be used for qemu shutdown, got %d calls", qmpClient.quitCalls)
	}

	if got, want := waiter.calls, 2; got != want {
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

func TestBuildQEMUSpecUsesTypedConfigAndRuntimeCID(t *testing.T) {
	manifest := validManifest("/tmp/work")

	spec, err := buildQEMUSpec(manifest, 42)
	if err != nil {
		t.Fatalf("build qemu spec: %v", err)
	}

	if spec.Path != "/bin/qemu-system-x86_64" {
		t.Fatalf("unexpected qemu path: got %q want %q", spec.Path, "/bin/qemu-system-x86_64")
	}
	if !containsString(spec.Args, "-name") || !containsString(spec.Args, "agent-sandbox") {
		t.Fatalf("expected qemu args to include the guest name: %v", spec.Args)
	}
	if !containsString(spec.Args, "guest-cid=42") {
		t.Fatalf("expected qemu args to include the runtime cid: %v", spec.Args)
	}
	if !containsString(spec.Args, "unix:/tmp/work/qmp.sock,server,nowait") {
		t.Fatalf("expected qemu args to include the qmp socket: %v", spec.Args)
	}
	if !containsString(spec.Args, "memory-backend-memfd,id=mem,size=1024M,share=on") {
		t.Fatalf("expected qemu args to include the shared memory backend: %v", spec.Args)
	}
}

func validManifest(workingDir string) *Manifest {
	return &Manifest{
		Identity: ManifestIdentity{HostName: "agent-sandbox"},
		Paths: ManifestPaths{
			WorkingDir: workingDir,
			LockPath:   "/tmp/virtie.lock",
		},
		SSH: ManifestSSH{
			Argv: []string{"/bin/ssh"},
			User: "agent",
		},
		QEMU: ManifestQEMU{
			BinaryPath: "/bin/qemu-system-x86_64",
			Name:       "agent-sandbox",
			Machine: ManifestQEMUMachine{
				Type:    "microvm",
				Options: []string{"accel=kvm:tcg"},
			},
			CPU: ManifestQEMUCPU{
				Model:     "host",
				EnableKVM: true,
			},
			Memory: ManifestQEMUMemory{
				SizeMiB: 1024,
				Backend: "memfd",
				Shared:  true,
			},
			Kernel: ManifestQEMUKernel{
				Path:       "/tmp/vmlinuz",
				InitrdPath: "/tmp/initrd",
				Params:     "panic=-1",
			},
			SMP: ManifestQEMUSMP{
				CPUs: 2,
			},
			Console: ManifestQEMUConsole{
				StdioChardev: true,
			},
			Knobs: ManifestQEMUKnobs{
				NoDefaults:   true,
				NoUserConfig: true,
				NoReboot:     true,
				NoGraphic:    true,
			},
			QMP: ManifestQEMUQMP{
				SocketPath: "qmp.sock",
			},
			Devices: ManifestQEMUDevices{
				RNG: ManifestQEMURNGDevice{
					ID:        "rng0",
					Transport: "pci",
				},
				VirtioFS: []ManifestQEMUVirtioFSShare{
					{
						ID:         "fs0",
						SocketPath: "fs.sock",
						Tag:        "workspace",
						Transport:  "pci",
					},
				},
				Block: []ManifestQEMUBlockDevice{
					{
						ID:        "vda",
						ImagePath: "root.img",
						AIO:       "threads",
						Transport: "pci",
					},
				},
				Network: []ManifestQEMUNetDevice{
					{
						ID:         "net0",
						Backend:    "user",
						MacAddress: "02:02:00:00:00:01",
						Transport:  "pci",
					},
				},
				VSOCK: ManifestQEMUVSOCKDevice{
					ID:        "vsock0",
					Transport: "pci",
				},
			},
		},
		Volumes: []ManifestVolume{
			{
				ImagePath:  "root.img",
				SizeMiB:    64,
				FSType:     "ext4",
				AutoCreate: true,
			},
		},
		VirtioFS: ManifestVirtioFS{Daemons: []ManifestVirtioFSDaemon{
			{
				Tag:        "workspace",
				SocketPath: "fs.sock",
				Command: ManifestCommand{
					Path: "/tmp/virtiofsd-workspace",
				},
			},
		}},
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
	qemu     *fakeProcess
}

func (r *fakeRunner) Start(spec ProcessSpec) (Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.starts = append(r.starts, spec.Name)

	switch spec.Name {
	case "qemu":
		r.qemuArgs = append([]string(nil), spec.Args...)
		r.qemuEnv = append([]string(nil), spec.Env...)
		process := &fakeProcess{name: spec.Name, runner: r, done: make(chan error, 1)}
		r.qemu = process
		return process, nil
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

func (r *fakeRunner) exitQEMU(err error) {
	r.mu.Lock()
	process := r.qemu
	r.mu.Unlock()
	if process == nil {
		return
	}
	process.complete(err)
}

type fakeProcess struct {
	name   string
	runner *fakeRunner
	done   chan error
	once   sync.Once
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
	p.complete(nil)
	return nil
}

func (p *fakeProcess) Kill() error {
	return p.Signal(nil)
}

func (p *fakeProcess) PID() int {
	return 1
}

func (p *fakeProcess) complete(err error) {
	p.once.Do(func() {
		select {
		case p.done <- err:
		default:
		}
		close(p.done)
	})
}

type fakeSocketWaiter struct {
	calls    int
	callback func(paths []string) error
}

func (w *fakeSocketWaiter) Wait(ctx context.Context, socketPaths []string) error {
	w.calls++
	return w.callback(socketPaths)
}

type fakeQMPDialer struct {
	client   QMPClient
	attempts int
}

func (d *fakeQMPDialer) Dial(ctx context.Context, socketPath string, timeout time.Duration) (QMPClient, error) {
	d.attempts++
	return d.client, nil
}

type fakeQMPClient struct {
	quitCalls int
	onQuit    func()
}

func (c *fakeQMPClient) Quit(timeout time.Duration) error {
	c.quitCalls++
	if c.onQuit != nil {
		c.onQuit()
	}
	return nil
}

func (c *fakeQMPClient) Disconnect() error {
	return nil
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
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
