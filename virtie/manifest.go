package virtie

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultVSockCIDStart = 3
	DefaultVSockCIDEnd   = 65535
)

type Manifest struct {
	Identity    ManifestIdentity    `json:"identity"`
	Paths       ManifestPaths       `json:"paths"`
	Persistence ManifestPersistence `json:"persistence"`
	SSH         ManifestSSH         `json:"ssh"`
	VSock       ManifestVSock       `json:"vsock"`
	VirtioFS    ManifestVirtioFS    `json:"virtiofs"`
}

type ManifestIdentity struct {
	HostName string `json:"hostName"`
}

type ManifestPaths struct {
	WorkingDir string `json:"workingDir"`
	MicroVMRun string `json:"microvmRun"`
	LockPath   string `json:"lockPath"`
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
	case m.Paths.MicroVMRun == "":
		return fmt.Errorf("manifest.paths.microvmRun is required")
	case m.Paths.LockPath == "":
		return fmt.Errorf("manifest.paths.lockPath is required")
	case len(m.SSH.Argv) == 0:
		return fmt.Errorf("manifest.ssh.argv must contain at least the ssh executable")
	case m.SSH.User == "":
		return fmt.Errorf("manifest.ssh.user is required")
	case m.VSock.CIDRange.Start < DefaultVSockCIDStart:
		return fmt.Errorf("manifest.vsock.cidRange.start must be at least %d", DefaultVSockCIDStart)
	case m.VSock.CIDRange.End < m.VSock.CIDRange.Start:
		return fmt.Errorf("manifest.vsock.cidRange.end must be greater than or equal to start")
	case len(m.VirtioFS.Daemons) == 0:
		return fmt.Errorf("manifest.virtiofs.daemons must contain at least one daemon")
	}

	for i, daemon := range m.VirtioFS.Daemons {
		if daemon.SocketPath == "" {
			return fmt.Errorf("manifest.virtiofs.daemons[%d].socketPath is required", i)
		}
		if daemon.Command.Path == "" {
			return fmt.Errorf("manifest.virtiofs.daemons[%d].command.path is required", i)
		}
	}

	return nil
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

func (m *Manifest) ResolvedSocketPaths() []string {
	paths := make([]string, 0, len(m.VirtioFS.Daemons))
	for _, daemon := range m.VirtioFS.Daemons {
		paths = append(paths, m.ResolvePath(daemon.SocketPath))
	}
	return paths
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

func (m *Manifest) ResolvedMicroVMRun() string {
	return m.ResolvePath(m.Paths.MicroVMRun)
}

func (m *Manifest) ResolvedVirtioFSDaemons() []ManifestVirtioFSDaemon {
	daemons := make([]ManifestVirtioFSDaemon, 0, len(m.VirtioFS.Daemons))
	for _, daemon := range m.VirtioFS.Daemons {
		resolved := daemon
		resolved.SocketPath = m.ResolvePath(daemon.SocketPath)
		resolved.Command.Path = m.ResolvePath(daemon.Command.Path)
		resolved.Command.Args = append([]string(nil), daemon.Command.Args...)
		daemons = append(daemons, resolved)
	}
	return daemons
}

func (m *Manifest) SSHDestination(cid int) string {
	return fmt.Sprintf("%s@vsock/%d", m.SSH.User, cid)
}
