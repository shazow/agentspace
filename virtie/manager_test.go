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

	"github.com/adrg/xdg"
	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
	balloonpkg "github.com/shazow/agentspace/virtie/balloon"
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

	balloon := validManifestWithBalloon("/tmp/work")
	if err := balloon.Validate(); err != nil {
		t.Fatalf("unexpected balloon controller validation error: %v", err)
	}
	if balloon.QEMU.Devices.Balloon.Controller == nil {
		t.Fatal("expected balloon controller defaults to be synthesized")
	}
	if got, want := balloon.QEMU.Devices.Balloon.Controller.MinActualMiB, 512; got != want {
		t.Fatalf("unexpected balloon controller minActualMiB default: got %d want %d", got, want)
	}
	if got, want := balloon.QEMU.Devices.Balloon.Controller.MaxActualMiB, balloon.QEMU.Memory.SizeMiB; got != want {
		t.Fatalf("unexpected balloon controller maxActualMiB default: got %d want %d", got, want)
	}
	if got, want := balloon.QEMU.Devices.Balloon.Controller.GrowBelowAvailableMiB, 256; got != want {
		t.Fatalf("unexpected balloon controller grow threshold default: got %d want %d", got, want)
	}
	if got, want := balloon.QEMU.Devices.Balloon.Controller.ReclaimAboveAvailableMiB, 512; got != want {
		t.Fatalf("unexpected balloon controller reclaim threshold default: got %d want %d", got, want)
	}
	if got, want := balloon.QEMU.Devices.Balloon.Controller.StepMiB, balloonpkg.DefaultControllerStepMiB; got != want {
		t.Fatalf("unexpected balloon controller step default: got %d want %d", got, want)
	}
	if got, want := balloon.QEMU.Devices.Balloon.Controller.PollIntervalSeconds, balloonpkg.DefaultControllerPollIntervalSecs; got != want {
		t.Fatalf("unexpected balloon controller poll interval default: got %d want %d", got, want)
	}
	if got, want := balloon.QEMU.Devices.Balloon.Controller.ReclaimHoldoffSeconds, balloonpkg.DefaultControllerReclaimHoldoff; got != want {
		t.Fatalf("unexpected balloon controller reclaim holdoff default: got %d want %d", got, want)
	}

	invalidBalloon := validManifestWithBalloon("/tmp/work")
	invalidBalloon.QEMU.Devices.Balloon.Controller = &balloonpkg.ControllerConfig{
		MinActualMiB: invalidBalloon.QEMU.Memory.SizeMiB + 1,
	}
	if err := invalidBalloon.Validate(); err == nil {
		t.Fatalf("expected validation error for invalid balloon controller bounds")
	}

	invalidThresholds := validManifestWithBalloon("/tmp/work")
	invalidThresholds.QEMU.Devices.Balloon.Controller = &balloonpkg.ControllerConfig{
		GrowBelowAvailableMiB:    512,
		ReclaimAboveAvailableMiB: 512,
	}
	if err := invalidThresholds.Validate(); err == nil {
		t.Fatalf("expected validation error for invalid balloon controller thresholds")
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

func TestManagerLaunchStartsBalloonControllerAfterSSHReadinessAndStopsItBeforeQuit(t *testing.T) {
	tmpDir := t.TempDir()
	manifest := validManifestWithBalloon(tmpDir)
	manifest.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")
	manifest.QEMU.Devices.Balloon.Controller = &balloonpkg.ControllerConfig{
		PollIntervalSeconds:   1,
		ReclaimHoldoffSeconds: 1,
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &fakeRunner{
		cancel:      cancel,
		cancelDelay: 2 * time.Second,
	}

	var enableProbes int
	var quitAt time.Time
	qmpClient := (&fakeQMPClient{
		onQuit: func() {
			quitAt = time.Now()
			runner.exitQEMU(nil)
		},
		onEnableBalloonStats: func() {
			runner.mu.Lock()
			enableProbes = runner.probes
			runner.mu.Unlock()
		},
		readBalloonStats: balloonpkg.Stats{
			Stats: map[string]int64{
				"stat-available-memory": int64(900) * balloonpkg.BytesPerMiB,
			},
			LastUpdate: time.Now(),
		},
		readBalloonStatsDelay: 400 * time.Millisecond,
		queryBalloonInfo:      balloonpkg.Info{ActualBytes: int64(512) * balloonpkg.BytesPerMiB},
	}).withDefaultBalloonPath("/machine/peripheral/balloon0")

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
		QMPConnectTimeout: time.Second,
		QMPQuitTimeout:    time.Second,
	}

	err := manager.Launch(cancelCtx, manifest, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	if enableProbes < 3 {
		t.Fatalf("expected balloon controller to start only after ssh readiness succeeded, got probe count %d", enableProbes)
	}
	if got, want := qmpClient.quitCount(), 1; got != want {
		t.Fatalf("expected qmp quit to be called once, got %d", got)
	}

	readCompleted := qmpClient.readCompletionTime()
	if readCompleted.IsZero() {
		t.Fatal("expected balloon controller poll to run before shutdown")
	}
	if quitAt.Before(readCompleted) {
		t.Fatalf("expected qmp quit after balloon controller stopped: quit=%s read-complete=%s", quitAt, readCompleted)
	}
}

func TestManagerLaunchDoesNotAbortOnBalloonControllerFailure(t *testing.T) {
	tmpDir := t.TempDir()
	manifest := validManifestWithBalloon(tmpDir)
	manifest.Paths.LockPath = filepath.Join(tmpDir, "virtie.lock")

	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner := &fakeRunner{cancel: cancel}
	qmpClient := (&fakeQMPClient{
		enableBalloonStatsErr: errors.New("guest stats unavailable"),
		onQuit: func() {
			runner.exitQEMU(nil)
		},
	}).withDefaultBalloonPath("/machine/peripheral/balloon0")

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
		QMPConnectTimeout: time.Second,
		QMPQuitTimeout:    time.Second,
	}

	err := manager.Launch(cancelCtx, manifest, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	if got, want := len(runner.sshArgs), 4; got != want {
		t.Fatalf("expected ssh session to start despite balloon controller failure, got %d ssh starts", got)
	}
	if got, want := qmpClient.quitCount(), 1; got != want {
		t.Fatalf("expected qmp quit to still be used on teardown, got %d", got)
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

func TestManifestResolvesSocketsFromRuntimeDir(t *testing.T) {
	runtimeDir := t.TempDir()
	setXDGTestRuntimeDir(t, runtimeDir)

	tests := []struct {
		name       string
		runtimeDir *string
		socketPath string
		wantSocket string
		wantQMP    string
	}{
		{
			name:       "legacy working dir",
			runtimeDir: nil,
			socketPath: "fs.sock",
			wantSocket: "/tmp/work/fs.sock",
			wantQMP:    "/tmp/work/qmp.sock",
		},
		{
			name:       "default runtime dir",
			runtimeDir: stringPtr(""),
			socketPath: "fs.sock",
			wantSocket: filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "fs.sock"),
			wantQMP:    filepath.Join(runtimeDir, "agentspace", "agent-sandbox", "qmp.sock"),
		},
		{
			name:       "relative runtime dir",
			runtimeDir: stringPtr("runtime"),
			socketPath: "fs.sock",
			wantSocket: "/tmp/work/runtime/fs.sock",
			wantQMP:    "/tmp/work/runtime/qmp.sock",
		},
		{
			name:       "absolute runtime dir",
			runtimeDir: stringPtr("/tmp/runtime"),
			socketPath: "fs.sock",
			wantSocket: "/tmp/runtime/fs.sock",
			wantQMP:    "/tmp/runtime/qmp.sock",
		},
		{
			name:       "absolute socket path bypasses runtime dir",
			runtimeDir: stringPtr(""),
			socketPath: "/tmp/explicit-fs.sock",
			wantSocket: "/tmp/explicit-fs.sock",
			wantQMP:    "/tmp/explicit-qmp.sock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := validManifest("/tmp/work")
			manifest.Paths.RuntimeDir = tt.runtimeDir
			manifest.VirtioFS.Daemons[0].SocketPath = tt.socketPath
			manifest.QEMU.Devices.VirtioFS[0].SocketPath = tt.socketPath
			if tt.name == "absolute socket path bypasses runtime dir" {
				manifest.QEMU.QMP.SocketPath = "/tmp/explicit-qmp.sock"
			}

			socketPaths, err := manifest.ResolvedSocketPaths()
			if err != nil {
				t.Fatalf("resolve socket paths: %v", err)
			}
			if got, want := socketPaths, []string{tt.wantSocket}; !reflect.DeepEqual(got, want) {
				t.Fatalf("unexpected socket paths: got %v want %v", got, want)
			}

			qmpSocketPath, err := manifest.ResolvedQMPSocketPath()
			if err != nil {
				t.Fatalf("resolve qmp socket path: %v", err)
			}
			if qmpSocketPath != tt.wantQMP {
				t.Fatalf("unexpected qmp socket path: got %q want %q", qmpSocketPath, tt.wantQMP)
			}

			qemu, err := manifest.ResolvedQEMU()
			if err != nil {
				t.Fatalf("resolve qemu config: %v", err)
			}
			if got, want := qemu.Devices.VirtioFS[0].SocketPath, tt.wantSocket; got != want {
				t.Fatalf("unexpected qemu virtiofs socket path: got %q want %q", got, want)
			}
			if got, want := qemu.QMP.SocketPath, tt.wantQMP; got != want {
				t.Fatalf("unexpected qemu qmp socket path: got %q want %q", got, want)
			}

			daemons, err := manifest.ResolvedVirtioFSDaemons()
			if err != nil {
				t.Fatalf("resolve virtiofs daemons: %v", err)
			}
			if got, want := daemons[0].SocketPath, tt.wantSocket; got != want {
				t.Fatalf("unexpected daemon socket path: got %q want %q", got, want)
			}
		})
	}
}

func TestStartVirtioFSDaemonsInjectsResolvedSocketPathEnv(t *testing.T) {
	runtimeDir := t.TempDir()
	setXDGTestRuntimeDir(t, runtimeDir)

	manifest := validManifest(t.TempDir())
	manifest.Paths.RuntimeDir = stringPtr("")

	runner := &fakeRunner{}
	manager := &Manager{
		Runner: runner,
		Logger: log.New(io.Discard, "", 0),
	}

	if _, err := manager.startVirtioFSDaemons(manifest); err != nil {
		t.Fatalf("start virtiofs daemons: %v", err)
	}

	wantSocket := filepath.Join(runtimeDir, "agentspace", manifest.Identity.HostName, "fs.sock")
	if got := runner.virtiofsEnv["virtiofsd[workspace]"]; !containsString(got, "VIRTIE_SOCKET_PATH="+wantSocket) {
		t.Fatalf("expected virtiofs daemon env to contain resolved socket path %q: %v", wantSocket, got)
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

func validManifestWithBalloon(workingDir string) *Manifest {
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
	mu                       sync.Mutex
	quitCalls                int
	onQuit                   func()
	onEnableBalloonStats     func()
	listQOMProperties        map[string][]balloonpkg.ObjectPropertyInfo
	listQOMPropertiesErr     map[string]error
	enableBalloonStatsErr    error
	queryBalloonInfo         balloonpkg.Info
	queryBalloonErr          error
	readBalloonStats         balloonpkg.Stats
	readBalloonStatsErr      error
	readBalloonStatsDelay    time.Duration
	readBalloonStatsComplete time.Time
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

func (c *fakeQMPClient) QueryBalloon(timeout time.Duration) (balloonpkg.Info, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.queryBalloonErr != nil {
		return balloonpkg.Info{}, c.queryBalloonErr
	}
	if c.queryBalloonInfo.ActualBytes == 0 {
		return balloonpkg.Info{ActualBytes: int64(512) * balloonpkg.BytesPerMiB}, nil
	}
	return c.queryBalloonInfo, nil
}

func (c *fakeQMPClient) SetBalloonLogicalSize(timeout time.Duration, logicalSizeBytes int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setBalloonLogicalSizes = append(c.setBalloonLogicalSizes, logicalSizeBytes)
	return c.setBalloonErr
}

func (c *fakeQMPClient) EnableBalloonStatsPolling(timeout time.Duration, qomPath string, pollIntervalSeconds int) error {
	c.mu.Lock()
	onEnable := c.onEnableBalloonStats
	err := c.enableBalloonStatsErr
	c.mu.Unlock()

	if onEnable != nil {
		onEnable()
	}
	return err
}

func (c *fakeQMPClient) ReadBalloonStats(timeout time.Duration, qomPath string) (balloonpkg.Stats, error) {
	c.mu.Lock()
	delay := c.readBalloonStatsDelay
	stats := c.readBalloonStats
	err := c.readBalloonStatsErr
	c.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	c.mu.Lock()
	c.readBalloonStatsComplete = time.Now()
	c.mu.Unlock()

	if err != nil {
		return balloonpkg.Stats{}, err
	}
	if stats.Stats == nil {
		stats.Stats = map[string]int64{
			"stat-available-memory": int64(768) * balloonpkg.BytesPerMiB,
		}
	}
	if stats.LastUpdate.IsZero() {
		stats.LastUpdate = time.Now()
	}
	return stats, nil
}

func (c *fakeQMPClient) ListQOMProperties(timeout time.Duration, path string) ([]balloonpkg.ObjectPropertyInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err, ok := c.listQOMPropertiesErr[path]; ok {
		return nil, err
	}
	if props, ok := c.listQOMProperties[path]; ok {
		return append([]balloonpkg.ObjectPropertyInfo(nil), props...), nil
	}
	return nil, errors.New("unexpected qom-list path")
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
	c.listQOMProperties = map[string][]balloonpkg.ObjectPropertyInfo{
		path: {
			{Name: "guest-stats", Type: "dict"},
			{Name: "guest-stats-polling-interval", Type: "int"},
		},
	}
	return c
}

func (c *fakeQMPClient) WithRaw(timeout time.Duration, fn func(*rawQMP.Monitor) error) error {
	return errors.New("unexpected raw qmp use in fakeQMPClient")
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
