package hotplug

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/executor"
)

func TestVirtioFSAttachSuccessWritesState(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, starter, qmp, guest := testRuntime(tmpDir, Device{
		Kind: KindVirtioFS,
		ID:   "cache",
		VirtioFS: VirtioFS{
			Source:     filepath.Join(tmpDir, "cache"),
			Target:     "/mnt/cache",
			SocketPath: filepath.Join(tmpDir, "cache.sock"),
			Bin:        "/bin/virtiofsd",
			Args:       []string{"--socket"},
		},
	})

	if err := runtime.Attach(context.Background(), "cache"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if got, want := starter.starts, []string{"virtiofsd"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("starts: got %#v want %#v", got, want)
	}
	if !strings.Contains(strings.Join(qmp.commands, "\n"), `"execute":"chardev-add"`) {
		t.Fatalf("expected chardev-add, got %#v", qmp.commands)
	}
	if got, want := guest.commands, [][]string{{"/run/current-system/sw/bin/mount", "-t", "virtiofs", "cache", "/mnt/cache"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("guest commands: got %#v want %#v", got, want)
	}
	state, err := ReadState(filepath.Join(tmpDir, "state", "hotplug", "cache.json"))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state.ID != "cache" || state.Kind != KindVirtioFS || state.Bus != "pcie.hotplug.0" || state.PID != 100 {
		t.Fatalf("unexpected state: %#v", state)
	}
}

func TestVirtioFSAttachQMPFailureRollsBackHost(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, starter, qmp, _ := testRuntime(tmpDir, testVirtioFSDevice(tmpDir))
	qmp.errAt = 2

	if err := runtime.Attach(context.Background(), "cache"); err == nil {
		t.Fatal("expected attach failure")
	}
	if got, want := starter.stopped, []int{100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stopped: got %#v want %#v", got, want)
	}
	if got := strings.Join(qmp.commands, "\n"); !strings.Contains(got, `"execute":"chardev-remove"`) {
		t.Fatalf("expected qmp rollback, got %#v", qmp.commands)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "state", "hotplug", "cache.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no state file, got %v", err)
	}
}

func TestVirtioFSAttachGuestFailureRollsBackQMPAndHost(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, starter, qmp, guest := testRuntime(tmpDir, testVirtioFSDevice(tmpDir))
	guest.err = errors.New("mount failed")

	if err := runtime.Attach(context.Background(), "cache"); err == nil {
		t.Fatal("expected attach failure")
	}
	if got, want := qmp.deviceDels, []string{"dev-cache"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("device dels: got %#v want %#v", got, want)
	}
	if got, want := starter.stopped, []int{100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stopped: got %#v want %#v", got, want)
	}
}

func TestVirtioFSDetachWaitsForDeviceDeletedBeforeChardevRemove(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, _, qmp, _ := testRuntime(tmpDir, testVirtioFSDevice(tmpDir))
	statePath := filepath.Join(tmpDir, "state", "hotplug", "cache.json")
	if err := WriteState(statePath, State{ID: "cache", Kind: KindVirtioFS, Bus: "pcie.hotplug.0", PID: 42}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	if err := runtime.Detach(context.Background(), "cache"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if got, want := qmp.events, []string{"device_del:dev-cache", "run:chardev-remove"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %#v want %#v", got, want)
	}
}

func TestNetAttachDetachCommands(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, _, qmp, _ := testRuntime(tmpDir, Device{
		Kind: KindNet,
		ID:   "vpn",
		Net: Net{
			Backend: "user",
			MAC:     "02:02:00:00:00:10",
			Forward: []Forward{{Proto: "tcp", Host: "127.0.0.1:2223", Guest: "10.0.2.15:22"}},
		},
	})

	if err := runtime.Attach(context.Background(), "vpn"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := runtime.Detach(context.Background(), "vpn"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	joined := strings.Join(qmp.commands, "\n")
	for _, want := range []string{`"execute":"netdev_add"`, `"execute":"device_add"`, `"execute":"netdev_del"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %s in qmp commands: %#v", want, qmp.commands)
		}
	}
	if got, want := qmp.deviceDels, []string{"dev-vpn"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("device dels: got %#v want %#v", got, want)
	}
}

func TestDetachRejectsStateKindMismatchBeforeCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, _, qmp, _ := testRuntime(tmpDir, Device{
		Kind: KindNet,
		ID:   "cache",
		Net:  Net{Backend: "user", MAC: "02:02:00:00:00:10"},
	})
	statePath := filepath.Join(tmpDir, "state", "hotplug", "cache.json")
	if err := WriteState(statePath, State{ID: "cache", Kind: KindVirtioFS, Bus: "pcie.hotplug.0", PID: 42}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	err := runtime.Detach(context.Background(), "cache")
	if err == nil || !strings.Contains(err.Error(), `kind "virtiofs", not current manifest kind "net"`) {
		t.Fatalf("expected kind mismatch error, got %v", err)
	}
	if len(qmp.commands) != 0 || len(qmp.deviceDels) != 0 {
		t.Fatalf("expected no qmp cleanup, got commands=%#v deviceDels=%#v", qmp.commands, qmp.deviceDels)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected state file to remain, got %v", err)
	}
}

func TestBlockAttachDetachCommands(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, _, qmp, _ := testRuntime(tmpDir, Device{
		Kind: KindBlock,
		ID:   "data",
		Block: Block{
			ImagePath: filepath.Join(tmpDir, "data.qcow2"),
			Format:    "qcow2",
			Serial:    "data",
		},
	})

	if err := runtime.Attach(context.Background(), "data"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := runtime.Detach(context.Background(), "data"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	joined := strings.Join(qmp.commands, "\n")
	for _, want := range []string{`"execute":"blockdev-add"`, `"execute":"device_add"`, `"execute":"blockdev-del"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %s in qmp commands: %#v", want, qmp.commands)
		}
	}
	if got, want := qmp.deviceDels, []string{"dev-data"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("device dels: got %#v want %#v", got, want)
	}
}

func TestHotplugRegistryKeysByInterfaceID(t *testing.T) {
	registry := make(hotplugRegistry)
	hotplug := fakeHotplug{id: "from-id"}

	if err := registry.add(hotplug); err != nil {
		t.Fatalf("add hotplug: %v", err)
	}
	got, err := registry.lookup("from-id")
	if err != nil {
		t.Fatalf("lookup hotplug: %v", err)
	}
	if got.ID() != "from-id" {
		t.Fatalf("unexpected hotplug id: got %q want from-id", got.ID())
	}
}

func TestHotplugRegistryMissingID(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, _, _, _ := testRuntime(tmpDir, testVirtioFSDevice(tmpDir))

	err := runtime.Attach(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), `manifest.hotplug id "missing" not found`) {
		t.Fatalf("expected missing id error, got %v", err)
	}
}

func TestHotplugRegistryRejectsDuplicateIDs(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, _, _, _ := testRuntimeDevices(tmpDir, []Device{
		testVirtioFSDevice(tmpDir),
		{
			Kind: KindNet,
			ID:   "cache",
			Net:  Net{Backend: "user", MAC: "02:02:00:00:00:10"},
		},
	})

	err := runtime.Attach(context.Background(), "cache")
	if err == nil || !strings.Contains(err.Error(), `manifest.hotplug id "cache" is duplicated`) {
		t.Fatalf("expected duplicate id error, got %v", err)
	}
}

func TestHotplugRegistryRejectsUnsupportedKind(t *testing.T) {
	tmpDir := t.TempDir()
	runtime, _, _, _ := testRuntimeDevices(tmpDir, []Device{
		testVirtioFSDevice(tmpDir),
		{Kind: Kind("unsupported"), ID: "bad"},
	})

	err := runtime.Attach(context.Background(), "cache")
	if err == nil || !strings.Contains(err.Error(), `manifest.hotplug id "bad" has unsupported kind "unsupported"`) {
		t.Fatalf("expected unsupported kind error, got %v", err)
	}
}

func testRuntime(tmpDir string, device Device) (Runtime, *fakeStarter, *fakeQMP, *fakeGuest) {
	return testRuntimeDevices(tmpDir, []Device{device})
}

func testRuntimeDevices(tmpDir string, devices []Device) (Runtime, *fakeStarter, *fakeQMP, *fakeGuest) {
	starter := &fakeStarter{}
	qmp := &fakeQMP{}
	guest := &fakeGuest{}
	return Runtime{
		StateDir: filepath.Join(tmpDir, "state"),
		WorkDir:  tmpDir,
		Devices:  devices,
		Start:    starter,
		Sockets:  fakeSockets{},
		QMP:      qmp,
		Guest:    guest,
	}, starter, qmp, guest
}

func testVirtioFSDevice(tmpDir string) Device {
	return Device{
		Kind: KindVirtioFS,
		ID:   "cache",
		VirtioFS: VirtioFS{
			Source:     filepath.Join(tmpDir, "cache"),
			Target:     "/mnt/cache",
			SocketPath: filepath.Join(tmpDir, "cache.sock"),
			Bin:        "/bin/virtiofsd",
		},
	}
}

type fakeHotplug struct {
	id string
}

func (h fakeHotplug) Attach() error { return nil }
func (h fakeHotplug) Detach() error { return nil }
func (h fakeHotplug) ID() string    { return h.id }

type fakeProcess struct {
	name string
	pid  int
}

func (p fakeProcess) PID() int { return p.pid }
func (p fakeProcess) Name() string {
	if p.name != "" {
		return p.name
	}
	return "fake"
}
func (p fakeProcess) Wait() error { return nil }
func (p fakeProcess) Signal(signal os.Signal) error {
	return nil
}
func (p fakeProcess) Kill() error { return nil }

type fakeStarter struct {
	starts  []string
	stopped []int
}

func (s *fakeStarter) Start(ctx context.Context, cmd *exec.Cmd) (*executor.Process, error) {
	name := filepath.Base(cmd.Path)
	if len(cmd.Args) > 0 && cmd.Args[0] != "" {
		name = filepath.Base(cmd.Args[0])
	}
	s.starts = append(s.starts, name)
	return executor.Wrap(fakeProcess{name: name, pid: 100}), nil
}

func (s *fakeStarter) Stop(process *executor.Process) error {
	s.stopped = append(s.stopped, process.PID())
	return nil
}

func (s *fakeStarter) SignalPIDGroup(pid int, signal syscall.Signal) error {
	s.stopped = append(s.stopped, pid)
	return nil
}

type fakeSockets struct{}

func (fakeSockets) Wait(ctx context.Context, stage string, socketPaths []string, process *executor.Process) error {
	return nil
}

type fakeQMP struct {
	commands   []string
	deviceDels []string
	events     []string
	errAt      int
}

func (q *fakeQMP) Run(ctx context.Context, command string) error {
	q.commands = append(q.commands, command)
	var message struct {
		Execute string `json:"execute"`
	}
	_ = jsonUnmarshal(command, &message)
	q.events = append(q.events, "run:"+message.Execute)
	if q.errAt > 0 && len(q.commands) == q.errAt {
		return errors.New("qmp failed")
	}
	return nil
}

func (q *fakeQMP) DeviceDel(ctx context.Context, id string) error {
	q.deviceDels = append(q.deviceDels, id)
	q.events = append(q.events, "device_del:"+id)
	return nil
}

type fakeGuest struct {
	commands [][]string
	err      error
}

func (g *fakeGuest) Run(ctx context.Context, command []string) error {
	g.commands = append(g.commands, append([]string(nil), command...))
	return g.err
}

func jsonUnmarshal(data string, v any) error {
	return json.Unmarshal([]byte(data), v)
}
