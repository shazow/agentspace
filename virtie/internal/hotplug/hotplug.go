package hotplug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Kind string

const (
	KindVirtioFS Kind = "virtiofs"
	KindNet      Kind = "net"
	KindBlock    Kind = "block"
)

type Device struct {
	Kind     Kind     `json:"kind"`
	ID       string   `json:"id"`
	VirtioFS VirtioFS `json:"virtiofs,omitempty"`
	Net      Net      `json:"net,omitempty"`
	Block    Block    `json:"block,omitempty"`
}

type VirtioFS struct {
	Source     string   `json:"source"`
	Target     string   `json:"target,omitempty"`
	SocketPath string   `json:"socketPath"`
	Bin        string   `json:"bin"`
	Args       []string `json:"args,omitempty"`
}

type Net struct {
	Backend string    `json:"backend"`
	MAC     string    `json:"mac"`
	Forward []Forward `json:"forward,omitempty"`
}

type Forward struct {
	Proto string `json:"proto"`
	Host  string `json:"host"`
	Guest string `json:"guest"`
}

type Block struct {
	ImagePath string `json:"imagePath"`
	Format    string `json:"format"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
	Serial    string `json:"serial,omitempty"`
}

type Hotplug interface {
	Attach() error
	Detach() error
	ID() string
}

type State struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	Bus  string `json:"bus"`
	PID  int    `json:"pid,omitempty"`
}

type Runtime struct {
	StateDir string
	WorkDir  string
	Devices  []Device
	Start    ProcessStarter
	Sockets  SocketWaiter
	QMP      QMPClient
	Guest    GuestRunner
}

type ProcessStarter interface {
	Start(ctx context.Context, spec ProcessSpec) (Process, error)
	Stop(process Process) error
	SignalPIDGroup(pid int, signal syscall.Signal) error
}

type Process interface {
	PID() int
}

type ProcessSpec struct {
	Name         string
	Path         string
	Args         []string
	Dir          string
	Env          []string
	ProcessGroup bool
}

type SocketWaiter interface {
	Wait(ctx context.Context, stage string, socketPaths []string, process Process) error
}

type QMPClient interface {
	Run(ctx context.Context, command string) error
	DeviceDel(ctx context.Context, id string) error
}

type GuestRunner interface {
	Run(ctx context.Context, command []string) error
}

func (r Runtime) Attach(ctx context.Context, id string) error {
	registry, err := r.hotplugRegistry(ctx)
	if err != nil {
		return err
	}
	hotplug, err := registry.lookup(id)
	if err != nil {
		return err
	}
	return hotplug.Attach()
}

func (r Runtime) Detach(ctx context.Context, id string) error {
	registry, err := r.hotplugRegistry(ctx)
	if err != nil {
		return err
	}
	hotplug, err := registry.lookup(id)
	if err != nil {
		return err
	}
	return hotplug.Detach()
}

type hotplugRegistry map[string]Hotplug

func (r Runtime) hotplugRegistry(ctx context.Context) (hotplugRegistry, error) {
	registry := make(hotplugRegistry, len(r.Devices))
	for i, device := range r.Devices {
		hotplug, err := r.hotplug(ctx, device, i)
		if err != nil {
			return nil, err
		}
		if err := registry.add(hotplug); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r Runtime) hotplug(ctx context.Context, device Device, index int) (Hotplug, error) {
	base := hotplugBase{
		ctx:     ctx,
		runtime: &r,
		id:      device.ID,
		kind:    device.Kind,
		bus:     fmt.Sprintf("pcie.hotplug.%d", index),
	}
	switch device.Kind {
	case KindVirtioFS:
		return &HotplugVirtioFS{hotplugBase: base, VirtioFS: device.VirtioFS}, nil
	case KindNet:
		return &HotplugNet{hotplugBase: base, Net: device.Net}, nil
	case KindBlock:
		return &HotplugBlock{hotplugBase: base, Block: device.Block}, nil
	default:
		return nil, fmt.Errorf("manifest.hotplug id %q has unsupported kind %q", device.ID, device.Kind)
	}
}

func (r hotplugRegistry) add(hotplug Hotplug) error {
	id := hotplug.ID()
	if _, ok := r[id]; ok {
		return fmt.Errorf("manifest.hotplug id %q is duplicated", id)
	}
	r[id] = hotplug
	return nil
}

func (r hotplugRegistry) lookup(id string) (Hotplug, error) {
	hotplug, ok := r[id]
	if !ok {
		return nil, fmt.Errorf("manifest.hotplug id %q not found", id)
	}
	return hotplug, nil
}

type hotplugBase struct {
	ctx     context.Context
	runtime *Runtime
	id      string
	kind    Kind
	bus     string
}

func (h hotplugBase) ID() string {
	return h.id
}

func (h hotplugBase) attach(device Device, attachHost func() (Process, error), detachHost func(Process)) error {
	if detachHost == nil {
		detachHost = func(Process) {}
	}
	statePath, err := StatePath(h.runtime.StateDir, h.id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(statePath); err == nil {
		return fmt.Errorf("hotplug %q is already attached; state exists at %q", h.id, statePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat hotplug state %q: %w", statePath, err)
	}

	var proc Process
	if attachHost != nil {
		proc, err = attachHost()
		if err != nil {
			return err
		}
	}

	state := State{ID: h.id, Kind: h.kind, Bus: h.bus}
	if proc != nil {
		state.PID = proc.PID()
	}
	if err := h.runtime.attachQMP(h.ctx, device, h.bus); err != nil {
		detachHost(proc)
		return err
	}
	if err := h.runtime.attachGuest(h.ctx, device); err != nil {
		_ = h.runtime.detachQMP(h.ctx, device)
		detachHost(proc)
		return err
	}
	if err := WriteState(statePath, state); err != nil {
		_ = h.runtime.detachGuest(h.ctx, device)
		_ = h.runtime.detachQMP(h.ctx, device)
		detachHost(proc)
		return err
	}
	return nil
}

func (h hotplugBase) detach(device Device, cleanup func(State) error) error {
	statePath, err := StatePath(h.runtime.StateDir, h.id)
	if err != nil {
		return err
	}
	state, err := ReadState(statePath)
	if err != nil {
		return err
	}
	if state.ID != h.id {
		return fmt.Errorf("hotplug state %q belongs to %q, not %q", statePath, state.ID, h.id)
	}

	if err := h.runtime.detachGuest(h.ctx, device); err != nil {
		return err
	}
	if err := h.runtime.detachQMP(h.ctx, device); err != nil {
		return err
	}
	if cleanup != nil {
		if err := cleanup(state); err != nil {
			return err
		}
	}
	if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove hotplug state %q: %w", statePath, err)
	}
	return nil
}

type HotplugVirtioFS struct {
	hotplugBase
	VirtioFS
}

func (h HotplugVirtioFS) Attach() error {
	return h.attach(h.device(), h.attachHost, h.detachHost)
}

func (h HotplugVirtioFS) Detach() error {
	return h.detach(h.device(), func(state State) error {
		if state.PID > 0 {
			if err := h.runtime.terminatePID(state.PID); err != nil {
				return err
			}
		}
		if h.SocketPath != "" {
			_ = os.Remove(h.SocketPath)
		}
		return nil
	})
}

func (h HotplugVirtioFS) device() Device {
	return Device{Kind: KindVirtioFS, ID: h.id, VirtioFS: h.VirtioFS}
}

func (h HotplugVirtioFS) attachHost() (Process, error) {
	return h.runtime.attachVirtioFSHost(h.ctx, h.device())
}

func (h HotplugVirtioFS) detachHost(proc Process) {
	h.runtime.rollbackHost(proc)
}

type HotplugNet struct {
	hotplugBase
	Net
}

func (h HotplugNet) Attach() error {
	return h.attach(h.device(), nil, nil)
}

func (h HotplugNet) Detach() error {
	return h.detach(h.device(), nil)
}

func (h HotplugNet) device() Device {
	return Device{Kind: KindNet, ID: h.id, Net: h.Net}
}

type HotplugBlock struct {
	hotplugBase
	Block
}

func (h HotplugBlock) Attach() error {
	return h.attach(h.device(), nil, nil)
}

func (h HotplugBlock) Detach() error {
	return h.detach(h.device(), nil)
}

func (h HotplugBlock) device() Device {
	return Device{Kind: KindBlock, ID: h.id, Block: h.Block}
}

func (r Runtime) attachVirtioFSHost(ctx context.Context, device Device) (Process, error) {
	fs := device.VirtioFS
	if r.Start == nil {
		return nil, fmt.Errorf("hotplug process starter is not configured")
	}
	proc, err := r.Start.Start(ctx, ProcessSpec{
		Name:         "hotplug[" + device.ID + "]",
		Path:         fs.Bin,
		Args:         fs.Args,
		Dir:          r.WorkDir,
		Env:          []string{"VIRTIOFSD_SOCKET=" + fs.SocketPath},
		ProcessGroup: true,
	})
	if err != nil {
		return nil, err
	}
	if fs.SocketPath != "" && r.Sockets != nil {
		if err := r.Sockets.Wait(ctx, "hotplug host exec", []string{fs.SocketPath}, proc); err != nil {
			_ = r.Start.Stop(proc)
			return nil, err
		}
	}
	return proc, nil
}

func (r Runtime) attachQMP(ctx context.Context, device Device, bus string) error {
	commands := attachCommands(device, bus)
	for i, command := range commands {
		if err := r.QMP.Run(ctx, command); err != nil {
			r.rollbackAttachQMP(ctx, device, i)
			return err
		}
	}
	return nil
}

func (r Runtime) rollbackAttachQMP(ctx context.Context, device Device, successful int) {
	if successful == 0 {
		return
	}
	for _, command := range rollbackAttachCommands(device, successful) {
		_ = r.QMP.Run(ctx, command)
	}
}

func (r Runtime) detachQMP(ctx context.Context, device Device) error {
	deviceID := qemuDeviceID(device.ID)
	if err := r.QMP.DeviceDel(ctx, deviceID); err != nil {
		return err
	}
	for _, command := range detachPostDeviceDelCommands(device) {
		if err := r.QMP.Run(ctx, command); err != nil {
			return err
		}
	}
	return nil
}

func (r Runtime) attachGuest(ctx context.Context, device Device) error {
	if device.Kind != KindVirtioFS || device.VirtioFS.Target == "" {
		return nil
	}
	return r.Guest.Run(ctx, []string{"/run/current-system/sw/bin/mount", "-t", "virtiofs", device.ID, device.VirtioFS.Target})
}

func (r Runtime) detachGuest(ctx context.Context, device Device) error {
	if device.Kind != KindVirtioFS || device.VirtioFS.Target == "" {
		return nil
	}
	return r.Guest.Run(ctx, []string{"/run/current-system/sw/bin/umount", device.VirtioFS.Target})
}

func (r Runtime) rollbackHost(proc Process) {
	if proc != nil && r.Start != nil {
		_ = r.Start.Stop(proc)
	}
}

func (r Runtime) terminatePID(pid int) error {
	if r.Start != nil {
		return r.Start.SignalPIDGroup(pid, syscall.SIGTERM)
	}
	return terminateProcessGroup(pid)
}

func StatePath(stateDir string, id string) (string, error) {
	if strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("hotplug id %q must not contain path separators", id)
	}
	return filepath.Join(stateDir, "hotplug", id+".json"), nil
}

func WriteState(path string, state State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create hotplug state directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode hotplug state: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write hotplug state %q: %w", path, err)
	}
	return nil
}

func ReadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, fmt.Errorf("hotplug state %q does not exist; is this device attached?", path)
		}
		return State{}, fmt.Errorf("read hotplug state %q: %w", path, err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("decode hotplug state %q: %w", path, err)
	}
	if state.ID == "" || state.Bus == "" || state.Kind == "" {
		return State{}, fmt.Errorf("invalid hotplug state %q", path)
	}
	return state, nil
}

func terminateProcessGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal hotplug pid %d: %w", pid, err)
	}
	return nil
}

func attachCommands(device Device, bus string) []string {
	switch device.Kind {
	case KindVirtioFS:
		return virtioFSAttachCommands(device, bus)
	case KindNet:
		return netAttachCommands(device, bus)
	case KindBlock:
		return blockAttachCommands(device, bus)
	default:
		return nil
	}
}

func detachPostDeviceDelCommands(device Device) []string {
	switch device.Kind {
	case KindVirtioFS:
		return []string{qmpCommand("chardev-remove", map[string]any{"id": charID(device.ID)})}
	case KindNet:
		return []string{qmpCommand("netdev_del", map[string]any{"id": netdevID(device.ID)})}
	case KindBlock:
		return []string{qmpCommand("blockdev-del", map[string]any{"node-name": blockNodeID(device.ID)})}
	default:
		return nil
	}
}

func rollbackAttachCommands(device Device, successful int) []string {
	if successful < 1 {
		return nil
	}
	switch device.Kind {
	case KindVirtioFS:
		return []string{qmpCommand("chardev-remove", map[string]any{"id": charID(device.ID)})}
	case KindNet:
		return []string{qmpCommand("netdev_del", map[string]any{"id": netdevID(device.ID)})}
	case KindBlock:
		return []string{qmpCommand("blockdev-del", map[string]any{"node-name": blockNodeID(device.ID)})}
	default:
		return nil
	}
}

func virtioFSAttachCommands(device Device, bus string) []string {
	id := device.ID
	return []string{
		qmpCommand("chardev-add", map[string]any{
			"id": charID(id),
			"backend": map[string]any{
				"type": "socket",
				"data": map[string]any{
					"addr":   map[string]any{"type": "unix", "data": map[string]any{"path": device.VirtioFS.SocketPath}},
					"server": false,
				},
			},
		}),
		qmpCommand("device_add", map[string]any{
			"driver":  "vhost-user-fs-pci",
			"id":      qemuDeviceID(id),
			"chardev": charID(id),
			"tag":     id,
			"bus":     bus,
		}),
	}
}

// netAttachCommands only attaches the QEMU side. Full networking support also
// needs guest-side link naming, DHCP or static address policy, and route setup.
func netAttachCommands(device Device, bus string) []string {
	id := device.ID
	netdev := map[string]any{"id": netdevID(id), "type": "user"}
	if len(device.Net.Forward) > 0 {
		hostfwd := make([]string, 0, len(device.Net.Forward))
		for _, forward := range device.Net.Forward {
			hostfwd = append(hostfwd, fmt.Sprintf("%s:%s-%s", forward.Proto, forward.Host, forward.Guest))
		}
		netdev["hostfwd"] = hostfwd
	}
	return []string{
		qmpCommand("netdev_add", netdev),
		qmpCommand("device_add", map[string]any{
			"driver": "virtio-net-pci",
			"id":     qemuDeviceID(id),
			"netdev": netdevID(id),
			"mac":    device.Net.MAC,
			"bus":    bus,
		}),
	}
}

// blockAttachCommands only attaches the QEMU block device. Full storage support
// also needs guest-side discovery, partition/filesystem policy, and mount setup.
func blockAttachCommands(device Device, bus string) []string {
	id := device.ID
	blockdev := map[string]any{
		"node-name": blockNodeID(id),
		"driver":    device.Block.Format,
		"file": map[string]any{
			"driver":   "file",
			"filename": device.Block.ImagePath,
		},
		"read-only": device.Block.ReadOnly,
	}
	deviceAdd := map[string]any{
		"driver": "virtio-blk-pci",
		"id":     qemuDeviceID(id),
		"drive":  blockNodeID(id),
		"bus":    bus,
	}
	if device.Block.Serial != "" {
		deviceAdd["serial"] = device.Block.Serial
	}
	return []string{
		qmpCommand("blockdev-add", blockdev),
		qmpCommand("device_add", deviceAdd),
	}
}

func qemuDeviceID(id string) string { return "dev-" + id }
func charID(id string) string       { return "char-" + id }
func netdevID(id string) string     { return "netdev-" + id }
func blockNodeID(id string) string  { return "block-" + id }

func qmpCommand(execute string, arguments map[string]any) string {
	payload := map[string]any{"execute": execute, "arguments": arguments}
	data, _ := json.Marshal(payload)
	return string(data)
}

func DefaultVirtioFSArgs(socketPath string, source string, id string) []string {
	return []string{
		"--socket-path=" + socketPath,
		"--shared-dir=" + source,
		"--tag=" + id,
	}
}
