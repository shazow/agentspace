package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/shazow/agentspace/virtie/balloon"
)

const (
	DefaultVSockCIDStart = 3
	DefaultVSockCIDEnd   = 65535
	DefaultVolumeFSType  = "ext4"
)

type Manifest struct {
	Identity    ManifestIdentity    `json:"identity"`
	Paths       ManifestPaths       `json:"paths"`
	Persistence ManifestPersistence `json:"persistence"`
	SSH         ManifestSSH         `json:"ssh"`
	QEMU        ManifestQEMU        `json:"qemu"`
	Volumes     []ManifestVolume    `json:"volumes,omitempty"`
	VSock       ManifestVSock       `json:"vsock"`
	VirtioFS    ManifestVirtioFS    `json:"virtiofs"`
}

type ManifestIdentity struct {
	HostName string `json:"hostName"`
}

type ManifestPaths struct {
	WorkingDir string  `json:"workingDir"`
	LockPath   string  `json:"lockPath"`
	RuntimeDir *string `json:"runtimeDir,omitempty"`
}

type ManifestPersistence struct {
	Directories []string `json:"directories"`
}

type ManifestSSH struct {
	Argv []string `json:"argv"`
	User string   `json:"user"`
}

type ManifestVSockCIDRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type ManifestVSock struct {
	CIDRange ManifestVSockCIDRange `json:"cidRange"`
}

type ManifestQEMU struct {
	BinaryPath      string              `json:"binaryPath"`
	Name            string              `json:"name"`
	Machine         ManifestQEMUMachine `json:"machine"`
	CPU             ManifestQEMUCPU     `json:"cpu"`
	Memory          ManifestQEMUMemory  `json:"memory"`
	Kernel          ManifestQEMUKernel  `json:"kernel"`
	SMP             ManifestQEMUSMP     `json:"smp"`
	Console         ManifestQEMUConsole `json:"console"`
	Knobs           ManifestQEMUKnobs   `json:"knobs"`
	QMP             ManifestQEMUQMP     `json:"qmp"`
	Devices         ManifestQEMUDevices `json:"devices"`
	MachineID       *string             `json:"machineId,omitempty"`
	PassthroughArgs []string            `json:"passthroughArgs,omitempty"`
}

type ManifestQEMUMachine struct {
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
}

type ManifestQEMUCPU struct {
	Model     string `json:"model"`
	EnableKVM bool   `json:"enableKvm,omitempty"`
}

type ManifestQEMUMemory struct {
	SizeMiB int    `json:"sizeMiB"`
	Backend string `json:"backend,omitempty"`
	Shared  bool   `json:"shared,omitempty"`
}

type ManifestQEMUKernel struct {
	Path       string `json:"path"`
	InitrdPath string `json:"initrdPath"`
	Params     string `json:"params,omitempty"`
}

type ManifestQEMUSMP struct {
	CPUs int `json:"cpus"`
}

type ManifestQEMUConsole struct {
	StdioChardev  bool `json:"stdioChardev,omitempty"`
	SerialConsole bool `json:"serialConsole,omitempty"`
}

type ManifestQEMUKnobs struct {
	NoDefaults     bool `json:"noDefaults,omitempty"`
	NoUserConfig   bool `json:"noUserConfig,omitempty"`
	NoReboot       bool `json:"noReboot,omitempty"`
	NoGraphic      bool `json:"noGraphic,omitempty"`
	SeccompSandbox bool `json:"seccompSandbox,omitempty"`
}

type ManifestQEMUQMP struct {
	SocketPath string `json:"socketPath"`
}

type ManifestQEMUDevices struct {
	RNG      ManifestQEMURNGDevice       `json:"rng"`
	I8042    bool                        `json:"i8042,omitempty"`
	Balloon  *balloon.Device             `json:"balloon,omitempty"`
	VirtioFS []ManifestQEMUVirtioFSShare `json:"virtiofs,omitempty"`
	Block    []ManifestQEMUBlockDevice   `json:"block,omitempty"`
	Network  []ManifestQEMUNetDevice     `json:"network,omitempty"`
	VSOCK    ManifestQEMUVSOCKDevice     `json:"vsock"`
}

type ManifestQEMURNGDevice struct {
	ID        string `json:"id"`
	Transport string `json:"transport"`
}

type ManifestQEMUVirtioFSShare struct {
	ID         string `json:"id"`
	SocketPath string `json:"socketPath"`
	Tag        string `json:"tag"`
	Transport  string `json:"transport"`
}

type ManifestQEMUBlockDevice struct {
	ID        string  `json:"id"`
	ImagePath string  `json:"imagePath"`
	AIO       string  `json:"aio,omitempty"`
	Cache     *string `json:"cache,omitempty"`
	ReadOnly  bool    `json:"readOnly,omitempty"`
	Serial    *string `json:"serial,omitempty"`
	Transport string  `json:"transport"`
}

type ManifestQEMUNetDevice struct {
	ID            string   `json:"id"`
	Backend       string   `json:"backend"`
	MacAddress    string   `json:"macAddress"`
	Transport     string   `json:"transport"`
	RomFile       *string  `json:"romFile,omitempty"`
	NetdevOptions []string `json:"netdevOptions,omitempty"`
	MQVectors     int      `json:"mqVectors,omitempty"`
}

type ManifestQEMUVSOCKDevice struct {
	ID        string `json:"id"`
	Transport string `json:"transport"`
}

type ManifestVolume struct {
	ImagePath     string   `json:"imagePath"`
	SizeMiB       int      `json:"sizeMiB,omitempty"`
	FSType        string   `json:"fsType,omitempty"`
	AutoCreate    bool     `json:"autoCreate,omitempty"`
	Label         *string  `json:"label,omitempty"`
	MkfsExtraArgs []string `json:"mkfsExtraArgs,omitempty"`
}

type ManifestCommand struct {
	Path string   `json:"path"`
	Args []string `json:"args,omitempty"`
}

type ManifestVirtioFSDaemon struct {
	Tag        string          `json:"tag"`
	SocketPath string          `json:"socketPath"`
	Command    ManifestCommand `json:"command"`
}

type ManifestVirtioFS struct {
	Daemons []ManifestVirtioFSDaemon `json:"daemons"`
}

func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %q: %w", path, err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest %q: %w", path, err)
	}

	if err := manifest.Validate(); err != nil {
		return nil, err
	}

	return &manifest, nil
}

func (m *Manifest) ApplyDefaults() {
	if m == nil {
		return
	}

	if m.VSock.CIDRange.Start == 0 {
		m.VSock.CIDRange.Start = DefaultVSockCIDStart
	}
	if m.VSock.CIDRange.End == 0 {
		m.VSock.CIDRange.End = DefaultVSockCIDEnd
	}

	for i := range m.Volumes {
		if m.Volumes[i].FSType == "" {
			m.Volumes[i].FSType = DefaultVolumeFSType
		}
	}

	applyBalloonDefaults(m.QEMU.Memory.SizeMiB, m.QEMU.Devices.Balloon)
}

func (m *Manifest) Validate() error {
	m.ApplyDefaults()

	switch {
	case m == nil:
		return fmt.Errorf("manifest is nil")
	case m.Identity.HostName == "":
		return fmt.Errorf("manifest.identity.hostName is required")
	case m.Paths.WorkingDir == "":
		return fmt.Errorf("manifest.paths.workingDir is required")
	case m.Paths.LockPath == "":
		return fmt.Errorf("manifest.paths.lockPath is required")
	case len(m.SSH.Argv) == 0:
		return fmt.Errorf("manifest.ssh.argv must contain at least the ssh executable")
	case m.SSH.User == "":
		return fmt.Errorf("manifest.ssh.user is required")
	case m.QEMU.BinaryPath == "":
		return fmt.Errorf("manifest.qemu.binaryPath is required")
	case m.QEMU.Name == "":
		return fmt.Errorf("manifest.qemu.name is required")
	case m.QEMU.Machine.Type == "":
		return fmt.Errorf("manifest.qemu.machine.type is required")
	case m.QEMU.CPU.Model == "":
		return fmt.Errorf("manifest.qemu.cpu.model is required")
	case m.QEMU.Memory.SizeMiB <= 0:
		return fmt.Errorf("manifest.qemu.memory.sizeMiB must be greater than zero")
	case m.QEMU.Kernel.Path == "":
		return fmt.Errorf("manifest.qemu.kernel.path is required")
	case m.QEMU.Kernel.InitrdPath == "":
		return fmt.Errorf("manifest.qemu.kernel.initrdPath is required")
	case m.QEMU.SMP.CPUs <= 0:
		return fmt.Errorf("manifest.qemu.smp.cpus must be greater than zero")
	case m.QEMU.QMP.SocketPath == "":
		return fmt.Errorf("manifest.qemu.qmp.socketPath is required")
	case m.VSock.CIDRange.Start < DefaultVSockCIDStart:
		return fmt.Errorf("manifest.vsock.cidRange.start must be at least %d", DefaultVSockCIDStart)
	case m.VSock.CIDRange.End < m.VSock.CIDRange.Start:
		return fmt.Errorf("manifest.vsock.cidRange.end must be greater than or equal to start")
	case len(m.VirtioFS.Daemons) == 0:
		return fmt.Errorf("manifest.virtiofs.daemons must contain at least one daemon")
	case m.QEMU.Devices.RNG.ID == "":
		return fmt.Errorf("manifest.qemu.devices.rng.id is required")
	case !validQEMUTransport(m.QEMU.Devices.RNG.Transport):
		return fmt.Errorf("manifest.qemu.devices.rng.transport must be one of pci, mmio, or ccw")
	case len(m.QEMU.Devices.VirtioFS) == 0:
		return fmt.Errorf("manifest.qemu.devices.virtiofs must contain at least one share")
	case len(m.QEMU.Devices.Block) == 0:
		return fmt.Errorf("manifest.qemu.devices.block must contain at least one device")
	case len(m.QEMU.Devices.Network) == 0:
		return fmt.Errorf("manifest.qemu.devices.network must contain at least one device")
	case m.QEMU.Devices.VSOCK.ID == "":
		return fmt.Errorf("manifest.qemu.devices.vsock.id is required")
	case !validQEMUTransport(m.QEMU.Devices.VSOCK.Transport):
		return fmt.Errorf("manifest.qemu.devices.vsock.transport must be one of pci, mmio, or ccw")
	}

	for i, daemon := range m.VirtioFS.Daemons {
		if daemon.SocketPath == "" {
			return fmt.Errorf("manifest.virtiofs.daemons[%d].socketPath is required", i)
		}
		if daemon.Command.Path == "" {
			return fmt.Errorf("manifest.virtiofs.daemons[%d].command.path is required", i)
		}
	}

	for i, share := range m.QEMU.Devices.VirtioFS {
		switch {
		case share.ID == "":
			return fmt.Errorf("manifest.qemu.devices.virtiofs[%d].id is required", i)
		case share.SocketPath == "":
			return fmt.Errorf("manifest.qemu.devices.virtiofs[%d].socketPath is required", i)
		case share.Tag == "":
			return fmt.Errorf("manifest.qemu.devices.virtiofs[%d].tag is required", i)
		case !validQEMUTransport(share.Transport):
			return fmt.Errorf("manifest.qemu.devices.virtiofs[%d].transport must be one of pci, mmio, or ccw", i)
		}
	}

	for i, block := range m.QEMU.Devices.Block {
		switch {
		case block.ID == "":
			return fmt.Errorf("manifest.qemu.devices.block[%d].id is required", i)
		case block.ImagePath == "":
			return fmt.Errorf("manifest.qemu.devices.block[%d].imagePath is required", i)
		case !validQEMUTransport(block.Transport):
			return fmt.Errorf("manifest.qemu.devices.block[%d].transport must be one of pci, mmio, or ccw", i)
		}
	}

	for i, netdev := range m.QEMU.Devices.Network {
		switch {
		case netdev.ID == "":
			return fmt.Errorf("manifest.qemu.devices.network[%d].id is required", i)
		case netdev.Backend != "user":
			return fmt.Errorf("manifest.qemu.devices.network[%d].backend must be \"user\"", i)
		case netdev.MacAddress == "":
			return fmt.Errorf("manifest.qemu.devices.network[%d].macAddress is required", i)
		case !validQEMUTransport(netdev.Transport):
			return fmt.Errorf("manifest.qemu.devices.network[%d].transport must be one of pci, mmio, or ccw", i)
		}
	}

	if err := validateBalloonDevice(m.QEMU.Memory.SizeMiB, m.QEMU.Devices.Balloon); err != nil {
		return err
	}

	for i, volume := range m.Volumes {
		if volume.ImagePath == "" {
			return fmt.Errorf("manifest.volumes[%d].imagePath is required", i)
		}
		if volume.AutoCreate && volume.SizeMiB <= 0 {
			return fmt.Errorf("manifest.volumes[%d].sizeMiB must be greater than zero when autoCreate is true", i)
		}
	}

	return nil
}

func validQEMUTransport(transport string) bool {
	switch transport {
	case "pci", "mmio", "ccw":
		return true
	default:
		return false
	}
}

func (m *Manifest) ResolvePath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(m.Paths.WorkingDir, path)
}

func (m *Manifest) ResolvedPersistenceDirectories() []string {
	dirs := make([]string, 0, len(m.Persistence.Directories))
	for _, dir := range m.Persistence.Directories {
		dirs = append(dirs, m.ResolvePath(dir))
	}
	return dirs
}

func (m *Manifest) resolveSocketPath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return path, nil
	}

	if m.Paths.RuntimeDir == nil {
		return m.ResolvePath(path), nil
	}

	if *m.Paths.RuntimeDir == "" {
		resolved, err := xdg.RuntimeFile(filepath.Join("agentspace", m.Identity.HostName, path))
		if err != nil {
			return "", fmt.Errorf("resolve runtime socket %q: %w", path, err)
		}
		return resolved, nil
	}

	return filepath.Join(m.ResolvePath(*m.Paths.RuntimeDir), path), nil
}

func (m *Manifest) ResolvedSocketPaths() ([]string, error) {
	paths := make([]string, 0, len(m.VirtioFS.Daemons))
	for _, daemon := range m.VirtioFS.Daemons {
		resolved, err := m.resolveSocketPath(daemon.SocketPath)
		if err != nil {
			return nil, err
		}
		paths = append(paths, resolved)
	}
	return paths, nil
}

func (m *Manifest) ResolvedLockPath() string {
	return m.ResolvePath(m.Paths.LockPath)
}

func (m *Manifest) ResolvedVSockLockDir() string {
	return filepath.Join(filepath.Dir(m.ResolvedLockPath()), "agentspace-vsock")
}

func (m *Manifest) ResolvedVSockLockPath(cid int) string {
	return filepath.Join(m.ResolvedVSockLockDir(), fmt.Sprintf("%d.lock", cid))
}

func (m *Manifest) ResolvedQMPSocketPath() (string, error) {
	return m.resolveSocketPath(m.QEMU.QMP.SocketPath)
}

func (m *Manifest) ResolvedQEMU() (ManifestQEMU, error) {
	resolved := m.QEMU
	resolved.BinaryPath = m.ResolvePath(resolved.BinaryPath)
	resolved.Kernel.Path = m.ResolvePath(resolved.Kernel.Path)
	resolved.Kernel.InitrdPath = m.ResolvePath(resolved.Kernel.InitrdPath)
	resolved.PassthroughArgs = append([]string(nil), resolved.PassthroughArgs...)

	qmpSocketPath, err := m.resolveSocketPath(resolved.QMP.SocketPath)
	if err != nil {
		return ManifestQEMU{}, err
	}
	resolved.QMP.SocketPath = qmpSocketPath

	if resolved.MachineID != nil {
		value := *resolved.MachineID
		resolved.MachineID = &value
	}

	resolved.Machine.Options = append([]string(nil), resolved.Machine.Options...)

	resolved.Devices.VirtioFS = append([]ManifestQEMUVirtioFSShare(nil), resolved.Devices.VirtioFS...)
	for i := range resolved.Devices.VirtioFS {
		socketPath, err := m.resolveSocketPath(resolved.Devices.VirtioFS[i].SocketPath)
		if err != nil {
			return ManifestQEMU{}, err
		}
		resolved.Devices.VirtioFS[i].SocketPath = socketPath
	}

	resolved.Devices.Block = append([]ManifestQEMUBlockDevice(nil), resolved.Devices.Block...)
	for i := range resolved.Devices.Block {
		resolved.Devices.Block[i].ImagePath = m.ResolvePath(resolved.Devices.Block[i].ImagePath)
		if resolved.Devices.Block[i].Cache != nil {
			value := *resolved.Devices.Block[i].Cache
			resolved.Devices.Block[i].Cache = &value
		}
		if resolved.Devices.Block[i].Serial != nil {
			value := *resolved.Devices.Block[i].Serial
			resolved.Devices.Block[i].Serial = &value
		}
	}

	resolved.Devices.Network = append([]ManifestQEMUNetDevice(nil), resolved.Devices.Network...)
	for i := range resolved.Devices.Network {
		resolved.Devices.Network[i].NetdevOptions = append([]string(nil), resolved.Devices.Network[i].NetdevOptions...)
		if resolved.Devices.Network[i].RomFile != nil {
			value := *resolved.Devices.Network[i].RomFile
			resolved.Devices.Network[i].RomFile = &value
		}
	}

	resolved.Devices.Balloon = cloneBalloonDevice(resolved.Devices.Balloon)

	return resolved, nil
}

func (m *Manifest) ResolvedVirtioFSDaemons() ([]ManifestVirtioFSDaemon, error) {
	daemons := make([]ManifestVirtioFSDaemon, 0, len(m.VirtioFS.Daemons))
	for _, daemon := range m.VirtioFS.Daemons {
		resolved := daemon
		socketPath, err := m.resolveSocketPath(daemon.SocketPath)
		if err != nil {
			return nil, err
		}
		resolved.SocketPath = socketPath
		resolved.Command.Path = m.ResolvePath(daemon.Command.Path)
		resolved.Command.Args = append([]string(nil), daemon.Command.Args...)
		daemons = append(daemons, resolved)
	}
	return daemons, nil
}

func (m *Manifest) ResolvedVolumes() []ManifestVolume {
	volumes := make([]ManifestVolume, 0, len(m.Volumes))
	for _, volume := range m.Volumes {
		resolved := volume
		resolved.ImagePath = m.ResolvePath(volume.ImagePath)
		resolved.MkfsExtraArgs = append([]string(nil), volume.MkfsExtraArgs...)
		volumes = append(volumes, resolved)
	}
	return volumes
}

func (m *Manifest) SSHDestination(cid int) string {
	return fmt.Sprintf("%s@vsock/%d", m.SSH.User, cid)
}
