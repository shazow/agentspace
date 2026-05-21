// Package manifest defines the internal virtie launch contract.
//
// It owns the JSON schema that Nix emits for virtie, along with the defaulting
// and validation rules that keep the runtime assumptions consistent. The
// package also resolves working-directory and runtime-directory paths into the
// concrete host-side paths that the manager uses for sockets, lock files,
// volumes, QEMU binaries, and virtiofs daemons.
package manifest

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/shazow/agentspace/virtie/internal/balloon"
)

const (
	defaultSSHRetryDelaySeconds = 0.5
	defaultSSHReadySocket       = ""
	defaultVSockCIDStart        = 3
	defaultVSockCIDEnd          = 65535
	defaultVolumeFSType         = "ext4"
	minAutoVolumeSizeMiB        = 256
)

var writeFileModePattern = regexp.MustCompile(`^0?[0-7]{3}$`)

type Manifest struct {
	Identity      Identity      `json:"identity"`
	Paths         Paths         `json:"paths"`
	Persistence   Persistence   `json:"persistence"`
	SSH           SSH           `json:"ssh"`
	QEMU          QEMU          `json:"qemu"`
	Volumes       []Volume      `json:"volumes,omitempty"`
	VSock         VSock         `json:"vsock"`
	VirtioFS      VirtioFS      `json:"virtiofs"`
	RunTunnels    []RunTunnel   `json:"runTunnels,omitempty"`
	WriteFiles    WriteFiles    `json:"writeFiles,omitempty"`
	Notifications Notifications `json:"notifications,omitempty"`
}

type Identity struct {
	HostName string `json:"hostName"`
}

type Paths struct {
	WorkingDir string  `json:"workingDir"`
	LockPath   string  `json:"lockPath"`
	RuntimeDir *string `json:"runtimeDir,omitempty"`
}

type Persistence struct {
	Directories []string `json:"directories"`
	BaseDir     string   `json:"baseDir,omitempty"`
	StateDir    string   `json:"stateDir,omitempty"`
}

type SSH struct {
	Argv          []string `json:"argv"`
	User          string   `json:"user"`
	RetryDelay    *float64 `json:"retryDelay,omitempty"`
	Autoprovision bool     `json:"autoprovision,omitempty"`
}

type VSockCIDRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type VSock struct {
	CIDRange VSockCIDRange `json:"cidRange"`
}

type QEMU struct {
	BinaryPath      string         `json:"binaryPath"`
	Name            string         `json:"name"`
	Machine         QEMUMachine    `json:"machine"`
	CPU             QEMUCPU        `json:"cpu"`
	Memory          QEMUMemory     `json:"memory"`
	Kernel          QEMUKernel     `json:"kernel"`
	SMP             QEMUSMP        `json:"smp"`
	Console         QEMUConsole    `json:"console"`
	Knobs           QEMUKnobs      `json:"knobs"`
	Graphics        *QEMUGraphics  `json:"graphics,omitempty"`
	QMP             QEMUQMP        `json:"qmp"`
	GuestAgent      QEMUGuestAgent `json:"guestAgent,omitempty"`
	SSHReady        QEMUSSHReady   `json:"sshReady,omitempty"`
	Devices         QEMUDevices    `json:"devices"`
	MachineID       *string        `json:"machineId,omitempty"`
	PassthroughArgs []string       `json:"passthroughArgs,omitempty"`
}

type QEMUMachine struct {
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
}

type QEMUCPU struct {
	Model     string `json:"model"`
	EnableKVM bool   `json:"enableKvm,omitempty"`
}

type QEMUMemory struct {
	SizeMiB int    `json:"sizeMiB"`
	Backend string `json:"backend,omitempty"`
	Shared  bool   `json:"shared,omitempty"`
}

type QEMUKernel struct {
	Path       string `json:"path"`
	InitrdPath string `json:"initrdPath"`
	Params     string `json:"params,omitempty"`
}

type QEMUSMP struct {
	CPUs *int `json:"cpus,omitempty"`
}

type QEMUConsole struct {
	StdioChardev  bool `json:"stdioChardev,omitempty"`
	SerialConsole bool `json:"serialConsole,omitempty"`
}

type QEMUKnobs struct {
	NoDefaults     bool  `json:"noDefaults,omitempty"`
	NoUserConfig   bool  `json:"noUserConfig,omitempty"`
	NoReboot       bool  `json:"noReboot,omitempty"`
	NoGraphic      *bool `json:"noGraphic,omitempty"`
	SeccompSandbox bool  `json:"seccompSandbox,omitempty"`
}

type QEMUGraphics struct {
	Backend string `json:"backend"`
}

type QEMUQMP struct {
	SocketPath string `json:"socketPath"`
}

type QEMUGuestAgent struct {
	SocketPath string `json:"socketPath,omitempty"`
}

type QEMUSSHReady struct {
	SocketPath string `json:"socketPath,omitempty"`
}

type QEMUDevices struct {
	RNG      QEMURNGDevice       `json:"rng"`
	I8042    bool                `json:"i8042,omitempty"`
	Balloon  *balloon.Device     `json:"balloon,omitempty"`
	VirtioFS []QEMUVirtioFSShare `json:"virtiofs,omitempty"`
	NineP    []QEMUNinePShare    `json:"9p,omitempty"`
	Block    []QEMUBlockDevice   `json:"block,omitempty"`
	Network  []QEMUNetDevice     `json:"network,omitempty"`
	VSOCK    QEMUVSOCKDevice     `json:"vsock"`
}

type QEMURNGDevice struct {
	ID        string `json:"id"`
	Transport string `json:"transport"`
}

type QEMUVirtioFSShare struct {
	ID         string `json:"id"`
	SocketPath string `json:"socketPath"`
	Tag        string `json:"tag"`
	Transport  string `json:"transport"`
}

type QEMUNinePShare struct {
	ID            string `json:"id"`
	SourcePath    string `json:"sourcePath"`
	Tag           string `json:"tag"`
	SecurityModel string `json:"securityModel"`
	ReadOnly      bool   `json:"readOnly"`
	Transport     string `json:"transport"`
}

type QEMUBlockDevice struct {
	ID        string  `json:"id"`
	ImagePath string  `json:"imagePath"`
	AIO       string  `json:"aio,omitempty"`
	Cache     *string `json:"cache,omitempty"`
	ReadOnly  bool    `json:"readOnly,omitempty"`
	Serial    *string `json:"serial,omitempty"`
	Transport string  `json:"transport"`
}

type QEMUNetDevice struct {
	ID            string   `json:"id"`
	Backend       string   `json:"backend"`
	MacAddress    string   `json:"macAddress"`
	Transport     string   `json:"transport"`
	RomFile       *string  `json:"romFile,omitempty"`
	NetdevOptions []string `json:"netdevOptions,omitempty"`
	MQVectors     int      `json:"mqVectors,omitempty"`
}

type QEMUVSOCKDevice struct {
	ID        string `json:"id"`
	Transport string `json:"transport"`
}

type Volume struct {
	ImagePath     string   `json:"imagePath"`
	SizeMiB       int      `json:"sizeMiB,omitempty"`
	FSType        string   `json:"fsType,omitempty"`
	AutoCreate    bool     `json:"autoCreate,omitempty"`
	Label         *string  `json:"label,omitempty"`
	MkfsExtraArgs []string `json:"mkfsExtraArgs,omitempty"`
}

type Command struct {
	Path string   `json:"path"`
	Args []string `json:"args,omitempty"`
}

type Notifications struct {
	Command *Command `json:"command,omitempty"`
	States  []string `json:"states,omitempty"`
}

type VirtioFSDaemon struct {
	Tag        string  `json:"tag"`
	SocketPath string  `json:"socketPath"`
	Command    Command `json:"command"`
}

type VirtioFS struct {
	Daemons []VirtioFSDaemon `json:"daemons"`
}

type RunTunnel struct {
	SocketPath string  `json:"socketPath"`
	Command    Command `json:"command"`
}

type WriteFile struct {
	Chown       *string `json:"chown,omitempty"`
	Text        *string `json:"text,omitempty"`
	Mode        *string `json:"mode,omitempty"`
	Overwrite   *bool   `json:"overwrite,omitempty"`
	FollowLinks *bool   `json:"followLinks,omitempty"`
	WriteBack   *bool   `json:"writeBack,omitempty"`
	Path        *string `json:"path,omitempty"`
}

type WriteFiles map[string]WriteFile

type ResolvedWriteFile struct {
	GuestPath   string
	Chown       *string
	Text        *string
	Mode        *string
	Overwrite   bool
	FollowLinks bool
	WriteBack   bool
	HostPath    *string
}

func Load(r io.Reader) (*Manifest, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return LoadBytes(data, "")
}

func (m *Manifest) applyDefaults() {
	if m == nil {
		return
	}

	if m.SSH.RetryDelay == nil {
		m.SSH.RetryDelay = float64Ptr(defaultSSHRetryDelaySeconds)
	}
	if m.QEMU.SSHReady.SocketPath == "" {
		m.QEMU.SSHReady.SocketPath = defaultSSHReadySocket
	}
	if m.QEMU.Knobs.NoGraphic == nil {
		m.QEMU.Knobs.NoGraphic = boolPtr(m.QEMU.Graphics == nil)
	}

	if m.VSock.CIDRange.Start == 0 {
		m.VSock.CIDRange.Start = defaultVSockCIDStart
	}
	if m.VSock.CIDRange.End == 0 {
		m.VSock.CIDRange.End = defaultVSockCIDEnd
	}

	for i := range m.Volumes {
		if m.Volumes[i].FSType == "" {
			m.Volumes[i].FSType = defaultVolumeFSType
		}
	}

	applyBalloonDefaults(m.QEMU.Memory.SizeMiB, m.QEMU.Devices.Balloon)
}

func (m *Manifest) Validate() error {
	m.applyDefaults()

	switch {
	case m == nil:
		return fmt.Errorf("manifest is nil")
	case m.Identity.HostName == "":
		return fmt.Errorf("manifest.identity.hostName is required")
	case m.Paths.WorkingDir == "":
		return fmt.Errorf("manifest.paths.workingDir is required")
	case m.Paths.LockPath == "":
		return fmt.Errorf("manifest.paths.lockPath is required")
	case m.SSH.User == "":
		return fmt.Errorf("manifest.ssh.user is required")
	case m.SSH.RetryDelay != nil && (math.IsNaN(*m.SSH.RetryDelay) || math.IsInf(*m.SSH.RetryDelay, 0) || *m.SSH.RetryDelay < 0):
		return fmt.Errorf("manifest.ssh.retryDelay must be a finite number greater than or equal to zero")
	case m.QEMU.BinaryPath == "":
		return fmt.Errorf("manifest.qemu.binaryPath is required")
	case m.QEMU.QMP.SocketPath == "":
		return fmt.Errorf("manifest.qemu.qmp.socketPath is required")
	case len(m.WriteFiles) > 0 && m.QEMU.GuestAgent.SocketPath == "":
		return fmt.Errorf("manifest.qemu.guestAgent.socketPath is required when manifest.writeFiles is set")
	case m.VSock.CIDRange.Start < defaultVSockCIDStart:
		return fmt.Errorf("manifest.vsock.cidRange.start must be at least %d", defaultVSockCIDStart)
	case m.VSock.CIDRange.End < m.VSock.CIDRange.Start:
		return fmt.Errorf("manifest.vsock.cidRange.end must be greater than or equal to start")
	case !validQEMUTransport(m.QEMU.Devices.RNG.Transport):
		return fmt.Errorf("manifest.qemu.devices.rng.transport must be one of pci, mmio, or ccw")
	case !validQEMUTransport(m.QEMU.Devices.VSOCK.Transport):
		return fmt.Errorf("manifest.qemu.devices.vsock.transport must be one of pci, mmio, or ccw")
	}

	if m.QEMU.Graphics != nil && !validQEMUGraphicsBackend(m.QEMU.Graphics.Backend) {
		return fmt.Errorf("manifest.qemu.graphics.backend must be one of gtk or cocoa")
	}

	virtioFSShareTags := make(map[string]struct{}, len(m.QEMU.Devices.VirtioFS))
	for _, share := range m.QEMU.Devices.VirtioFS {
		virtioFSShareTags[share.Tag] = struct{}{}
	}
	for i, daemon := range m.VirtioFS.Daemons {
		if daemon.Tag == "" {
			return fmt.Errorf("manifest.virtiofs.daemons[%d].tag is required", i)
		}
		if _, ok := virtioFSShareTags[daemon.Tag]; !ok {
			return fmt.Errorf("manifest.virtiofs.daemons[%d].tag must match a qemu virtiofs share", i)
		}
		if daemon.SocketPath == "" {
			return fmt.Errorf("manifest.virtiofs.daemons[%d].socketPath is required", i)
		}
		if daemon.Command.Path == "" {
			return fmt.Errorf("manifest.virtiofs.daemons[%d].command.path is required", i)
		}
	}

	seenTunnelSockets := make(map[string]int, len(m.RunTunnels))
	for i, tunnel := range m.RunTunnels {
		switch {
		case tunnel.SocketPath == "":
			return fmt.Errorf("manifest.runTunnels[%d].socketPath is required", i)
		case !cleanRelativePath(tunnel.SocketPath):
			return fmt.Errorf("manifest.runTunnels[%d].socketPath must be a clean relative path under state_dir/tunnels", i)
		case tunnel.Command.Path == "":
			return fmt.Errorf("manifest.runTunnels[%d].command.path is required", i)
		}
		if previous, ok := seenTunnelSockets[tunnel.SocketPath]; ok {
			return fmt.Errorf("manifest.runTunnels[%d].socketPath duplicates manifest.runTunnels[%d]", i, previous)
		}
		seenTunnelSockets[tunnel.SocketPath] = i
	}

	for i, share := range m.QEMU.Devices.VirtioFS {
		switch {
		case share.SocketPath == "":
			return fmt.Errorf("manifest.qemu.devices.virtiofs[%d].socketPath is required", i)
		case !validQEMUTransport(share.Transport):
			return fmt.Errorf("manifest.qemu.devices.virtiofs[%d].transport must be one of pci, mmio, or ccw", i)
		}
	}

	for i, share := range m.QEMU.Devices.NineP {
		if !validQEMUTransport(share.Transport) {
			return fmt.Errorf("manifest.qemu.devices.9p[%d].transport must be one of pci, mmio, or ccw", i)
		}
	}

	for i, block := range m.QEMU.Devices.Block {
		if !validQEMUTransport(block.Transport) {
			return fmt.Errorf("manifest.qemu.devices.block[%d].transport must be one of pci, mmio, or ccw", i)
		}
	}

	for i, netdev := range m.QEMU.Devices.Network {
		if !validQEMUTransport(netdev.Transport) {
			return fmt.Errorf("manifest.qemu.devices.network[%d].transport must be one of pci, mmio, or ccw", i)
		}
	}

	if err := validateBalloonDevice(m.QEMU.Memory.SizeMiB, m.QEMU.Devices.Balloon); err != nil {
		return err
	}

	if err := validateWriteFiles(m.WriteFiles); err != nil {
		return err
	}
	for i, volume := range m.Volumes {
		if volume.AutoCreate && volume.ImagePath == "" {
			return fmt.Errorf("manifest.volumes[%d].imagePath is required", i)
		}
		if volume.AutoCreate && volume.SizeMiB <= 0 {
			return fmt.Errorf("manifest.volumes[%d].sizeMiB must be greater than zero when autoCreate is true", i)
		}
		if volume.AutoCreate && volume.SizeMiB < minAutoVolumeSizeMiB {
			return fmt.Errorf("manifest.volumes[%d].sizeMiB must be at least %d when autoCreate is true", i, minAutoVolumeSizeMiB)
		}
		if volume.AutoCreate && volume.FSType != defaultVolumeFSType {
			return fmt.Errorf("manifest.volumes[%d].fsType must be %q when autoCreate is true", i, defaultVolumeFSType)
		}
		if volume.AutoCreate && len(volume.MkfsExtraArgs) > 0 {
			return fmt.Errorf("manifest.volumes[%d].mkfsExtraArgs is not supported when autoCreate is true", i)
		}
	}

	return nil
}

func (m *Manifest) SSHRetryDelay(fallback time.Duration) time.Duration {
	if m == nil || m.SSH.RetryDelay == nil {
		return fallback
	}
	return time.Duration(*m.SSH.RetryDelay * float64(time.Second))
}

func validateWriteFiles(files WriteFiles) error {
	paths := make([]string, 0, len(files))
	for guestPath := range files {
		paths = append(paths, guestPath)
	}
	sort.Strings(paths)

	for _, guestPath := range paths {
		entry := files[guestPath]
		switch {
		case guestPath == "":
			return fmt.Errorf("manifest.writeFiles contains an empty guest path")
		case !filepath.IsAbs(guestPath):
			return fmt.Errorf("manifest.writeFiles[%q] guest path must be absolute", guestPath)
		case (entry.Text == nil) == (entry.Path == nil):
			return fmt.Errorf("manifest.writeFiles[%q] must set exactly one of text or path", guestPath)
		case entry.Path != nil && *entry.Path == "":
			return fmt.Errorf("manifest.writeFiles[%q].path must not be empty", guestPath)
		case writeFileWriteBack(entry) && entry.Path == nil:
			return fmt.Errorf("manifest.writeFiles[%q].writeBack requires path", guestPath)
		case entry.Mode != nil && !writeFileModePattern.MatchString(*entry.Mode):
			return fmt.Errorf("manifest.writeFiles[%q].mode must match ^0?[0-7]{3}$", guestPath)
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

func validQEMUGraphicsBackend(backend string) bool {
	switch backend {
	case "gtk", "cocoa":
		return true
	default:
		return false
	}
}

func cleanRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || path == "." || filepath.Clean(path) != path {
		return false
	}
	return path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func (m *Manifest) resolvePath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}

	return filepath.Join(m.Paths.WorkingDir, path)
}

func (m *Manifest) ResolvedPersistenceDirectories() []string {
	dirs := make([]string, 0, len(m.Persistence.Directories))
	for _, dir := range m.Persistence.Directories {
		dirs = append(dirs, m.resolvePath(dir))
	}
	return dirs
}

func (m *Manifest) ResolvedPersistenceBaseDir() string {
	if m.Persistence.BaseDir == "" {
		return m.resolvePath(".")
	}
	return m.resolvePath(m.Persistence.BaseDir)
}

func (m *Manifest) ResolvedPersistenceStateDir() string {
	if m.Persistence.StateDir != "" {
		return m.resolvePath(m.Persistence.StateDir)
	}
	return m.ResolvedPersistenceBaseDir()
}

func (m *Manifest) resolveSocketPath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return path, nil
	}

	if m.Paths.RuntimeDir == nil {
		return m.resolvePath(path), nil
	}

	if *m.Paths.RuntimeDir == "" {
		resolved, err := xdg.RuntimeFile(filepath.Join("agentspace", m.Identity.HostName, path))
		if err != nil {
			return "", fmt.Errorf("resolve runtime socket %q: %w", path, err)
		}
		return resolved, nil
	}

	return filepath.Join(m.resolvePath(*m.Paths.RuntimeDir), path), nil
}

func (m *Manifest) resolveStatePath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Join(m.ResolvedPersistenceStateDir(), path), nil
}

func (m *Manifest) resolveTunnelSocketPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(m.ResolvedPersistenceStateDir(), "tunnels", path)
}

func (m *Manifest) ResolvedSocketPaths() ([]string, error) {
	paths := make([]string, 0, len(m.VirtioFS.Daemons)+len(m.RunTunnels))
	for _, daemon := range m.VirtioFS.Daemons {
		resolved, err := m.resolveSocketPath(daemon.SocketPath)
		if err != nil {
			return nil, err
		}
		paths = append(paths, resolved)
	}
	tunnelPaths, err := m.ResolvedRunTunnelSocketPaths()
	if err != nil {
		return nil, err
	}
	paths = append(paths, tunnelPaths...)
	return paths, nil
}

func (m *Manifest) ResolvedRunTunnelSocketPaths() ([]string, error) {
	paths := make([]string, 0, len(m.RunTunnels))
	for _, tunnel := range m.RunTunnels {
		paths = append(paths, m.resolveTunnelSocketPath(tunnel.SocketPath))
	}
	return paths, nil
}

func (m *Manifest) ResolvedVirtioFSSocketPaths() ([]string, error) {
	paths := make([]string, 0, len(m.QEMU.Devices.VirtioFS))
	for _, share := range m.QEMU.Devices.VirtioFS {
		resolved, err := m.resolveSocketPath(share.SocketPath)
		if err != nil {
			return nil, err
		}
		paths = append(paths, resolved)
	}
	return paths, nil
}

func (m *Manifest) ResolvedExternalVirtioFSSocketPaths() ([]string, error) {
	managedTags := make(map[string]struct{}, len(m.VirtioFS.Daemons))
	for _, daemon := range m.VirtioFS.Daemons {
		managedTags[daemon.Tag] = struct{}{}
	}

	paths := make([]string, 0, len(m.QEMU.Devices.VirtioFS))
	for _, share := range m.QEMU.Devices.VirtioFS {
		if _, managed := managedTags[share.Tag]; managed {
			continue
		}
		resolved, err := m.resolveSocketPath(share.SocketPath)
		if err != nil {
			return nil, err
		}
		paths = append(paths, resolved)
	}
	return paths, nil
}

func (m *Manifest) ResolvedLockPath() string {
	return m.resolvePath(m.Paths.LockPath)
}

func (m *Manifest) ResolvedVSockLockPath(cid int) string {
	lockPath := m.ResolvedLockPath()
	lockName := strings.TrimSuffix(filepath.Base(lockPath), ".lock")
	return filepath.Join(filepath.Dir(lockPath), fmt.Sprintf("%s-vsock-%d.lock", lockName, cid))
}

func (m *Manifest) ResolvedQMPSocketPath() (string, error) {
	return m.resolveSocketPath(m.QEMU.QMP.SocketPath)
}

func (m *Manifest) ResolvedGuestAgentSocketPath() (string, error) {
	return m.resolveSocketPath(m.QEMU.GuestAgent.SocketPath)
}

func (m *Manifest) ResolvedSSHReadySocketPath() (string, error) {
	return m.resolveSocketPath(m.QEMU.SSHReady.SocketPath)
}

func (m *Manifest) ResolvedQEMU() (QEMU, error) {
	resolved := m.QEMU
	resolved.BinaryPath = m.resolvePath(resolved.BinaryPath)
	resolved.Kernel.Path = m.resolvePath(resolved.Kernel.Path)
	resolved.Kernel.InitrdPath = m.resolvePath(resolved.Kernel.InitrdPath)
	resolved.PassthroughArgs = append([]string(nil), resolved.PassthroughArgs...)

	qmpSocketPath, err := m.resolveSocketPath(resolved.QMP.SocketPath)
	if err != nil {
		return QEMU{}, err
	}
	resolved.QMP.SocketPath = qmpSocketPath

	guestAgentSocketPath, err := m.resolveSocketPath(resolved.GuestAgent.SocketPath)
	if err != nil {
		return QEMU{}, err
	}
	resolved.GuestAgent.SocketPath = guestAgentSocketPath

	sshReadySocketPath, err := m.resolveSocketPath(resolved.SSHReady.SocketPath)
	if err != nil {
		return QEMU{}, err
	}
	resolved.SSHReady.SocketPath = sshReadySocketPath

	if resolved.MachineID != nil {
		value := *resolved.MachineID
		resolved.MachineID = &value
	}

	resolved.Machine.Options = append([]string(nil), resolved.Machine.Options...)
	if resolved.Graphics != nil {
		value := *resolved.Graphics
		resolved.Graphics = &value
	}

	resolved.Devices.VirtioFS = append([]QEMUVirtioFSShare(nil), resolved.Devices.VirtioFS...)
	for i := range resolved.Devices.VirtioFS {
		socketPath, err := m.resolveSocketPath(resolved.Devices.VirtioFS[i].SocketPath)
		if err != nil {
			return QEMU{}, err
		}
		resolved.Devices.VirtioFS[i].SocketPath = socketPath
	}

	resolved.Devices.NineP = append([]QEMUNinePShare(nil), resolved.Devices.NineP...)
	for i := range resolved.Devices.NineP {
		resolved.Devices.NineP[i].SourcePath = m.resolvePath(resolved.Devices.NineP[i].SourcePath)
	}

	resolved.Devices.Block = append([]QEMUBlockDevice(nil), resolved.Devices.Block...)
	for i := range resolved.Devices.Block {
		resolved.Devices.Block[i].ImagePath = m.resolvePath(resolved.Devices.Block[i].ImagePath)
		if resolved.Devices.Block[i].Cache != nil {
			value := *resolved.Devices.Block[i].Cache
			resolved.Devices.Block[i].Cache = &value
		}
		if resolved.Devices.Block[i].Serial != nil {
			value := *resolved.Devices.Block[i].Serial
			resolved.Devices.Block[i].Serial = &value
		}
	}

	resolved.Devices.Network = append([]QEMUNetDevice(nil), resolved.Devices.Network...)
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

func (m *Manifest) ResolvedVirtioFSDaemons() ([]VirtioFSDaemon, error) {
	daemons := make([]VirtioFSDaemon, 0, len(m.VirtioFS.Daemons))
	for _, daemon := range m.VirtioFS.Daemons {
		resolved := daemon
		socketPath, err := m.resolveSocketPath(daemon.SocketPath)
		if err != nil {
			return nil, err
		}
		resolved.SocketPath = socketPath
		resolved.Command.Path = m.resolvePath(daemon.Command.Path)
		resolved.Command.Args = append([]string(nil), daemon.Command.Args...)
		daemons = append(daemons, resolved)
	}
	return daemons, nil
}

func (m *Manifest) ResolvedRunTunnels() ([]RunTunnel, error) {
	tunnels := make([]RunTunnel, 0, len(m.RunTunnels))
	for _, tunnel := range m.RunTunnels {
		resolved := tunnel
		resolved.SocketPath = m.resolveTunnelSocketPath(tunnel.SocketPath)
		resolved.Command.Path = m.resolvePath(tunnel.Command.Path)
		resolved.Command.Args = append([]string(nil), tunnel.Command.Args...)
		tunnels = append(tunnels, resolved)
	}
	return tunnels, nil
}

func (m *Manifest) ResolvedNotifications() Notifications {
	resolved := Notifications{
		States: append([]string(nil), m.Notifications.States...),
	}
	if m.Notifications.Command != nil {
		command := *m.Notifications.Command
		command.Path = m.resolvePath(command.Path)
		command.Args = append([]string(nil), m.Notifications.Command.Args...)
		resolved.Command = &command
	}
	return resolved
}

func (m *Manifest) ResolvedVolumes() []Volume {
	volumes := make([]Volume, 0, len(m.Volumes))
	for _, volume := range m.Volumes {
		resolved := volume
		resolved.ImagePath = m.resolvePath(volume.ImagePath)
		resolved.MkfsExtraArgs = append([]string(nil), volume.MkfsExtraArgs...)
		volumes = append(volumes, resolved)
	}
	return volumes
}

func (m *Manifest) ResolvedWriteFiles() []ResolvedWriteFile {
	paths := make([]string, 0, len(m.WriteFiles))
	for guestPath := range m.WriteFiles {
		paths = append(paths, guestPath)
	}
	sort.Strings(paths)

	files := make([]ResolvedWriteFile, 0, len(paths))
	for _, guestPath := range paths {
		entry := m.WriteFiles[guestPath]
		resolved := ResolvedWriteFile{
			GuestPath:   guestPath,
			Chown:       entry.Chown,
			Text:        entry.Text,
			Mode:        entry.Mode,
			Overwrite:   writeFileOverwrite(entry),
			FollowLinks: writeFileFollowLinks(entry),
			WriteBack:   writeFileWriteBack(entry),
		}
		if entry.Path != nil {
			hostPath := m.resolvePath(*entry.Path)
			resolved.HostPath = &hostPath
		}
		files = append(files, resolved)
	}
	return files
}

func writeFileOverwrite(file WriteFile) bool {
	return file.Overwrite != nil && *file.Overwrite
}

func writeFileFollowLinks(file WriteFile) bool {
	return file.FollowLinks == nil || *file.FollowLinks
}

func writeFileWriteBack(file WriteFile) bool {
	return file.WriteBack != nil && *file.WriteBack
}

func (m *Manifest) SSHDestination(cid int) string {
	return fmt.Sprintf("%s@vsock/%d", m.SSH.User, cid)
}

func intPtr(value int) *int {
	return &value
}

func float64Ptr(value float64) *float64 {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func stringPtr(value string) *string {
	return &value
}

func (q QEMU) NoGraphicEnabled() bool {
	if q.Knobs.NoGraphic != nil {
		return *q.Knobs.NoGraphic
	}
	return q.Graphics == nil
}
