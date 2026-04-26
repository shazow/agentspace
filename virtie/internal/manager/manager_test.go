package manager

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/adrg/xdg"
	doQMP "github.com/digitalocean/go-qemu/qmp"
	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
	balloonpkg "github.com/shazow/agentspace/virtie/internal/balloon"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

const testMiB int64 = 1024 * 1024

func TestManifestValidate(t *testing.T) {
	emptyManifest := &manifest.Manifest{}
	if err := emptyManifest.Validate(); err == nil {
		t.Fatalf("expected validation error for empty manifest")
	}

	valid := validManifest("/tmp/work")

	if err := valid.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if got, want := valid.VSock.CIDRange.Start, 3; got != want {
		t.Fatalf("unexpected default vsock start: got %d want %d", got, want)
	}
	if got, want := valid.VSock.CIDRange.End, 65535; got != want {
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
	manifest := &manifest.Manifest{
		Paths: manifest.Paths{
			WorkingDir: "/tmp/work",
		},
		SSH: manifest.SSH{
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
		"-o",
		"LogLevel=ERROR",
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
	cfg := validManifest(tmpDir)
	cfg.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")
	cfg.Persistence.Directories = []string{"persist"}
	cfg.QEMU.QMP.SocketPath = "qmp.sock"
	cfg.QEMU.Devices.Block[0].ImagePath = "overlay.img"
	cfg.Volumes = []manifest.Volume{
		{
			ImagePath:  "overlay.img",
			SizeMiB:    64,
			FSType:     "ext4",
			AutoCreate: true,
		},
	}
	cfg.VirtioFS.Daemons = []manifest.VirtioFSDaemon{
		{
			Tag:        "ro-store",
			SocketPath: "sock-a",
			Command: manifest.Command{
				Path: "/bin/virtiofsd-ro-store",
			},
		},
		{
			Tag:        "workspace",
			SocketPath: "sock-b",
			Command: manifest.Command{
				Path: "/bin/virtiofsd-workspace",
			},
		},
	}
	cfg.QEMU.Devices.VirtioFS = []manifest.QEMUVirtioFSShare{
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

	manager := &manager{
		locker:            &fileLocker{},
		runner:            runner,
		socketWaiter:      waiter,
		qmpDialer:         &fakeQMPDialer{client: qmpClient},
		logger:            log.New(io.Discard, "", 0),
		sshRetryDelay:     0,
		shutdownDelay:     10 * time.Millisecond,
		qmpRetryDelay:     0,
		qmpConnectTimeout: time.Millisecond,
		qmpQuitTimeout:    time.Millisecond,
	}

	err := manager.launch(cancelCtx, cfg, nil)
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
	if containsString(runner.qemuArgs, "balloon") {
		t.Fatalf("expected qemu args to omit optional feature devices when disabled: %v", runner.qemuArgs)
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

func TestManagerLaunchUsesExternalVirtioFSSocketWithoutManagingDaemon(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := validManifest(tmpDir)
	cfg.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")
	cfg.QEMU.Devices.Block[0].ImagePath = "root.img"
	cfg.Volumes[0].AutoCreate = false
	externalSocket := filepath.Join(tmpDir, "virtiofs-nix-store.sock")
	if err := os.WriteFile(externalSocket, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write external socket placeholder: %v", err)
	}
	cfg.QEMU.Devices.VirtioFS[0].SocketPath = externalSocket
	cfg.VirtioFS.Daemons = nil

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
			return nil
		},
	}
	manager := &manager{
		locker:            &fileLocker{},
		runner:            runner,
		socketWaiter:      waiter,
		qmpDialer:         &fakeQMPDialer{client: qmpClient},
		logger:            log.New(io.Discard, "", 0),
		sshRetryDelay:     0,
		shutdownDelay:     10 * time.Millisecond,
		qmpConnectTimeout: time.Millisecond,
		qmpQuitTimeout:    time.Millisecond,
	}

	err := manager.launch(cancelCtx, cfg, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	if containsString(runner.starts, "virtiofsd[workspace]") {
		t.Fatalf("unexpected managed virtiofsd start for external socket: %v", runner.starts)
	}
	if _, err := os.Stat(externalSocket); err != nil {
		t.Fatalf("expected external socket path to be left alone: %v", err)
	}
	if len(waiter.paths) == 0 || !reflect.DeepEqual(waiter.paths[0], []string{externalSocket}) {
		t.Fatalf("expected virtiofs readiness wait to use external socket, got %v", waiter.paths)
	}
}

func TestWaitForSSHAbortsInFlightProbeOnCancellation(t *testing.T) {
	runner := &blockingSSHRunner{started: make(chan *blockingSSHProcess, 1)}
	manager := &manager{
		runner:        runner,
		logger:        log.New(io.Discard, "", 0),
		sshRetryDelay: time.Second,
	}

	manifest := &manifest.Manifest{
		Paths: manifest.Paths{
			WorkingDir: t.TempDir(),
		},
		SSH: manifest.SSH{
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

func TestWaitForSSHLogsPermissionDeniedHint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &permissionDeniedThenCancelSSHRunner{cancel: cancel}
	var logOutput bytes.Buffer
	manager := &manager{
		runner:        runner,
		logger:        log.New(&logOutput, "", 0),
		sshRetryDelay: 0,
	}

	manifest := &manifest.Manifest{
		Paths: manifest.Paths{
			WorkingDir: t.TempDir(),
		},
		SSH: manifest.SSH{
			Argv: []string{"/bin/ssh"},
			User: "agent",
		},
	}

	defer cancel()

	if err := manager.waitForSSH(ctx, manifest, 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	logs := logOutput.String()
	if !strings.Contains(logs, "Permission denied (publickey)") {
		t.Fatalf("expected permission denied hint in logs, got %q", logs)
	}
	if !strings.Contains(logs, "ssh-add") {
		t.Fatalf("expected ssh-add hint in logs, got %q", logs)
	}
}

func TestSSHPermissionDeniedMatchesPublicKeyAuthFailures(t *testing.T) {
	tests := []struct {
		stderr string
		want   bool
	}{
		{stderr: "agent@vsock/10: Permission denied (publickey).\n", want: true},
		{stderr: "ssh: connect to host vsock/10 port 22: Connection refused\n", want: false},
		{stderr: "Connection timed out during banner exchange\n", want: false},
	}

	for _, tt := range tests {
		if got := sshPermissionDenied(tt.stderr); got != tt.want {
			t.Fatalf("sshPermissionDenied(%q) = %v, want %v", tt.stderr, got, tt.want)
		}
	}
}

func TestAllocateCIDSkipsLockedIDs(t *testing.T) {
	tmpDir := t.TempDir()
	manifest := &manifest.Manifest{
		Paths: manifest.Paths{
			WorkingDir: tmpDir,
			LockPath:   filepath.Join(tmpDir, "virtie.lock"),
		},
		VSock: manifest.VSock{
			CIDRange: manifest.VSockCIDRange{
				Start: 7,
				End:   8,
			},
		},
	}

	locker := &fileLocker{}
	held, err := locker.Acquire(manifest.ResolvedVSockLockPath(7))
	if err != nil {
		t.Fatalf("acquire held cid lock: %v", err)
	}
	defer held.Release()

	manager := &manager{locker: locker}
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

func TestBuildQEMUSpecUsesRuntimeDirForRelativeQMP(t *testing.T) {
	runtimeDir := t.TempDir()
	setXDGTestRuntimeDir(t, runtimeDir)

	manifest := validManifest("/tmp/work")
	manifest.Paths.RuntimeDir = stringPtr("")

	spec, err := buildQEMUSpec(manifest, 42)
	if err != nil {
		t.Fatalf("build qemu spec: %v", err)
	}

	wantQMP := filepath.Join(runtimeDir, "agentspace", manifest.Identity.HostName, "qmp.sock")
	if !containsString(spec.Args, "unix:"+wantQMP+",server,nowait") {
		t.Fatalf("expected qemu args to include runtime qmp socket %q: %v", wantQMP, spec.Args)
	}
}

func TestStartVirtioFSDaemonsInjectsResolvedSocketPathEnv(t *testing.T) {
	runtimeDir := t.TempDir()
	setXDGTestRuntimeDir(t, runtimeDir)

	manifest := validManifest(t.TempDir())
	manifest.Paths.RuntimeDir = stringPtr("")

	runner := &fakeRunner{}
	manager := &manager{
		runner: runner,
		logger: log.New(io.Discard, "", 0),
	}

	if _, err := manager.startVirtioFSDaemons(manifest); err != nil {
		t.Fatalf("start virtiofs daemons: %v", err)
	}

	wantSocket := filepath.Join(runtimeDir, "agentspace", manifest.Identity.HostName, "fs.sock")
	if got := runner.virtiofsEnv["virtiofsd[workspace]"]; !containsString(got, "VIRTIE_SOCKET_PATH="+wantSocket) {
		t.Fatalf("expected virtiofs daemon env to contain resolved socket path %q: %v", wantSocket, got)
	}
}

func validManifest(workingDir string) *manifest.Manifest {
	return &manifest.Manifest{
		Identity: manifest.Identity{HostName: "agent-sandbox"},
		Paths: manifest.Paths{
			WorkingDir: workingDir,
			LockPath:   "/tmp/virtie.lock",
		},
		SSH: manifest.SSH{
			Argv: []string{"/bin/ssh"},
			User: "agent",
		},
		QEMU: manifest.QEMU{
			BinaryPath: "/bin/qemu-system-x86_64",
			Name:       "agent-sandbox",
			Machine: manifest.QEMUMachine{
				Type:    "microvm",
				Options: []string{"accel=kvm:tcg"},
			},
			CPU: manifest.QEMUCPU{
				Model:     "host",
				EnableKVM: true,
			},
			Memory: manifest.QEMUMemory{
				SizeMiB: 1024,
				Backend: "memfd",
				Shared:  true,
			},
			Kernel: manifest.QEMUKernel{
				Path:       "/tmp/vmlinuz",
				InitrdPath: "/tmp/initrd",
				Params:     "panic=-1",
			},
			SMP: manifest.QEMUSMP{
				CPUs: 2,
			},
			Console: manifest.QEMUConsole{
				StdioChardev: true,
			},
			Knobs: manifest.QEMUKnobs{
				NoDefaults:   true,
				NoUserConfig: true,
				NoReboot:     true,
				NoGraphic:    true,
			},
			QMP: manifest.QEMUQMP{
				SocketPath: "qmp.sock",
			},
			Devices: manifest.QEMUDevices{
				RNG: manifest.QEMURNGDevice{
					ID:        "rng0",
					Transport: "pci",
				},
				VirtioFS: []manifest.QEMUVirtioFSShare{
					{
						ID:         "fs0",
						SocketPath: "fs.sock",
						Tag:        "workspace",
						Transport:  "pci",
					},
				},
				Block: []manifest.QEMUBlockDevice{
					{
						ID:        "vda",
						ImagePath: "root.img",
						AIO:       "threads",
						Transport: "pci",
					},
				},
				Network: []manifest.QEMUNetDevice{
					{
						ID:         "net0",
						Backend:    "user",
						MacAddress: "02:02:00:00:00:01",
						Transport:  "pci",
					},
				},
				VSOCK: manifest.QEMUVSOCKDevice{
					ID:        "vsock0",
					Transport: "pci",
				},
			},
		},
		Volumes: []manifest.Volume{
			{
				ImagePath:  "root.img",
				SizeMiB:    64,
				FSType:     "ext4",
				AutoCreate: true,
			},
		},
		VirtioFS: manifest.VirtioFS{Daemons: []manifest.VirtioFSDaemon{
			{
				Tag:        "workspace",
				SocketPath: "fs.sock",
				Command: manifest.Command{
					Path: "/tmp/virtiofsd-workspace",
				},
			},
		}},
	}
}

func validManifestWithBalloon(workingDir string) *manifest.Manifest {
	manifest := validManifest(workingDir)
	manifest.QEMU.Devices.Balloon = &balloonpkg.Device{
		ID:        "balloon0",
		Transport: "pci",
	}
	return manifest
}

type fakeRunner struct {
	mu          sync.Mutex
	starts      []string
	signals     []string
	sshArgs     [][]string
	qemuArgs    []string
	qemuEnv     []string
	virtiofsEnv map[string][]string
	probes      int
	cancel      context.CancelFunc
	cancelDelay time.Duration
	qemu        *fakeProcess
}

func (r *fakeRunner) Start(spec processSpec) (process, error) {
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
		go func() {
			if r.cancelDelay > 0 {
				time.Sleep(r.cancelDelay)
			}
			r.cancel()
		}()
		return process, nil
	default:
		if strings.HasPrefix(spec.Name, "virtiofsd[") {
			if r.virtiofsEnv == nil {
				r.virtiofsEnv = make(map[string][]string)
			}
			r.virtiofsEnv[spec.Name] = append([]string(nil), spec.Env...)
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
	paths    [][]string
	callback func(paths []string) error
}

func (w *fakeSocketWaiter) Wait(ctx context.Context, socketPaths []string) error {
	w.calls++
	w.paths = append(w.paths, append([]string(nil), socketPaths...))
	return w.callback(socketPaths)
}

type fakeQMPDialer struct {
	client   qmpClient
	attempts int
}

func (d *fakeQMPDialer) Dial(ctx context.Context, socketPath string, timeout time.Duration) (qmpClient, error) {
	d.attempts++
	return d.client, nil
}

type fakeQMPClient struct {
	mu                       sync.Mutex
	quitCalls                int
	onQuit                   func()
	onEnableBalloonStats     func()
	listQOMProperties        map[string][]fakeQOMProperty
	listQOMPropertiesErr     map[string]error
	enableBalloonStatsErr    error
	queryBalloonActualBytes  int64
	queryBalloonErr          error
	readBalloonStats         map[string]int64
	readBalloonStatsErr      error
	readBalloonStatsDelay    time.Duration
	readBalloonStatsComplete time.Time
	readBalloonStatsUpdated  time.Time
	setBalloonLogicalSizes   []int64
	setBalloonErr            error
}

func (c *fakeQMPClient) Quit(timeout time.Duration) error {
	c.mu.Lock()
	c.quitCalls++
	onQuit := c.onQuit
	c.mu.Unlock()

	if onQuit != nil {
		onQuit()
	}
	return nil
}

func (c *fakeQMPClient) readCompletionTime() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readBalloonStatsComplete
}

func (c *fakeQMPClient) quitCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quitCalls
}

func (c *fakeQMPClient) Disconnect() error {
	return nil
}

func (c *fakeQMPClient) withDefaultBalloonPath(path string) *fakeQMPClient {
	c.listQOMProperties = map[string][]fakeQOMProperty{
		path: {
			{Name: "guest-stats", Type: "dict"},
			{Name: "guest-stats-polling-interval", Type: "int"},
		},
	}
	return c
}

func (c *fakeQMPClient) WithRaw(timeout time.Duration, fn func(*rawQMP.Monitor) error) error {
	return fn(rawQMP.NewMonitor(&fakeMonitor{handler: c.handleQMP}))
}

type fakeQOMProperty struct {
	Name string
	Type string
}

type fakeMonitor struct {
	handler func(message map[string]any) (map[string]any, error)
}

func (m *fakeMonitor) Connect() error {
	return nil
}

func (m *fakeMonitor) Disconnect() error {
	return nil
}

func (m *fakeMonitor) Run(command []byte) ([]byte, error) {
	var message map[string]any
	if err := json.Unmarshal(command, &message); err != nil {
		return nil, err
	}

	response := map[string]any{"return": map[string]any{}}
	var err error
	if m.handler != nil {
		response, err = m.handler(message)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(response)
}

func (m *fakeMonitor) Events(context.Context) (<-chan doQMP.Event, error) {
	return nil, doQMP.ErrEventsNotSupported
}

func (c *fakeQMPClient) handleQMP(message map[string]any) (map[string]any, error) {
	command, _ := message["execute"].(string)
	args, _ := message["arguments"].(map[string]any)

	switch command {
	case "quit":
		c.mu.Lock()
		c.quitCalls++
		onQuit := c.onQuit
		c.mu.Unlock()
		if onQuit != nil {
			onQuit()
		}
		return map[string]any{"return": map[string]any{}}, nil
	case "query-balloon":
		c.mu.Lock()
		actualBytes := c.queryBalloonActualBytes
		err := c.queryBalloonErr
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if actualBytes == 0 {
			actualBytes = 512 * testMiB
		}
		return map[string]any{"return": map[string]any{"actual": actualBytes}}, nil
	case "balloon":
		value, _ := args["value"].(float64)
		c.mu.Lock()
		c.setBalloonLogicalSizes = append(c.setBalloonLogicalSizes, int64(value))
		err := c.setBalloonErr
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return map[string]any{"return": map[string]any{}}, nil
	case "qom-set":
		property, _ := args["property"].(string)
		if property == "guest-stats-polling-interval" {
			c.mu.Lock()
			onEnable := c.onEnableBalloonStats
			err := c.enableBalloonStatsErr
			c.mu.Unlock()
			if onEnable != nil {
				onEnable()
			}
			if err != nil {
				return nil, err
			}
		}
		return map[string]any{"return": map[string]any{}}, nil
	case "qom-get":
		c.mu.Lock()
		delay := c.readBalloonStatsDelay
		err := c.readBalloonStatsErr
		snapshot := mapsClone(c.readBalloonStats)
		updated := c.readBalloonStatsUpdated
		c.mu.Unlock()

		if delay > 0 {
			time.Sleep(delay)
		}

		c.mu.Lock()
		c.readBalloonStatsComplete = time.Now()
		c.mu.Unlock()

		if err != nil {
			return nil, err
		}
		if len(snapshot) == 0 {
			snapshot = map[string]int64{
				"stat-available-memory": 768 * testMiB,
			}
		}
		if updated.IsZero() {
			updated = time.Now()
		}
		return map[string]any{
			"return": map[string]any{
				"stats":       snapshot,
				"last-update": updated.Unix(),
			},
		}, nil
	case "qom-list":
		path, _ := args["path"].(string)
		c.mu.Lock()
		err := c.listQOMPropertiesErr[path]
		props, ok := c.listQOMProperties[path]
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("unexpected qom-list path")
		}
		entries := make([]map[string]any, 0, len(props))
		for _, prop := range props {
			entries = append(entries, map[string]any{
				"name": prop.Name,
				"type": prop.Type,
			})
		}
		return map[string]any{"return": entries}, nil
	default:
		return nil, errors.New("unexpected qmp command")
	}
}

func mapsClone(src map[string]int64) map[string]int64 {
	if src == nil {
		return nil
	}
	dst := make(map[string]int64, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
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

func (r *blockingSSHRunner) Start(spec processSpec) (process, error) {
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

type permissionDeniedThenCancelSSHRunner struct {
	mu         sync.Mutex
	startCount int
	cancel     context.CancelFunc
}

func (r *permissionDeniedThenCancelSSHRunner) Start(spec processSpec) (process, error) {
	if spec.Name != "ssh" {
		return nil, errors.New("unexpected process")
	}

	r.mu.Lock()
	r.startCount++
	startCount := r.startCount
	r.mu.Unlock()

	if startCount == 1 {
		if spec.Stderr != nil {
			_, _ = io.WriteString(spec.Stderr, "agent@vsock/10: Permission denied (publickey).\n")
		}
		return &fakeProcess{
			name: spec.Name,
			done: closedErrorChannel(errors.New("exit status 255")),
		}, nil
	}

	process := &blockingSSHProcess{done: make(chan error, 1)}
	r.cancel()
	return process, nil
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func stringPtr(value string) *string {
	return &value
}

func setXDGTestRuntimeDir(t *testing.T, runtimeDir string) {
	t.Helper()

	original, hadOriginal := os.LookupEnv("XDG_RUNTIME_DIR")
	if err := os.Setenv("XDG_RUNTIME_DIR", runtimeDir); err != nil {
		t.Fatalf("set XDG_RUNTIME_DIR: %v", err)
	}
	xdg.Reload()

	t.Cleanup(func() {
		var err error
		if hadOriginal {
			err = os.Setenv("XDG_RUNTIME_DIR", original)
		} else {
			err = os.Unsetenv("XDG_RUNTIME_DIR")
		}
		if err != nil {
			t.Fatalf("restore XDG_RUNTIME_DIR: %v", err)
		}
		xdg.Reload()
	})
}
