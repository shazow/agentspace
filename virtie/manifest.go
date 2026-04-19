package virtie

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Manifest struct {
	Identity    ManifestIdentity    `json:"identity"`
	Paths       ManifestPaths       `json:"paths"`
	Persistence ManifestPersistence `json:"persistence"`
	SSH         ManifestSSH         `json:"ssh"`
	VirtioFS    ManifestVirtioFS    `json:"virtiofs"`
}

type ManifestIdentity struct {
	HostName string `json:"hostName"`
}

type ManifestPaths struct {
	WorkingDir   string `json:"workingDir"`
	MicroVMRun   string `json:"microvmRun"`
	VirtioFSDRun string `json:"virtiofsdRun"`
	LockPath     string `json:"lockPath"`
}

type ManifestPersistence struct {
	Directories []string `json:"directories"`
}

type ManifestSSH struct {
	Argv []string `json:"argv"`
}

type ManifestVirtioFS struct {
	SocketPaths []string `json:"socketPaths"`
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

func (m *Manifest) Validate() error {
	switch {
	case m == nil:
		return fmt.Errorf("manifest is nil")
	case m.Identity.HostName == "":
		return fmt.Errorf("manifest.identity.hostName is required")
	case m.Paths.WorkingDir == "":
		return fmt.Errorf("manifest.paths.workingDir is required")
	case m.Paths.MicroVMRun == "":
		return fmt.Errorf("manifest.paths.microvmRun is required")
	case m.Paths.VirtioFSDRun == "":
		return fmt.Errorf("manifest.paths.virtiofsdRun is required")
	case m.Paths.LockPath == "":
		return fmt.Errorf("manifest.paths.lockPath is required")
	case len(m.SSH.Argv) == 0:
		return fmt.Errorf("manifest.ssh.argv must contain at least the ssh executable")
	case len(m.VirtioFS.SocketPaths) == 0:
		return fmt.Errorf("manifest.virtiofs.socketPaths must contain at least one socket path")
	default:
		return nil
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

func (m *Manifest) ResolvedSocketPaths() []string {
	paths := make([]string, 0, len(m.VirtioFS.SocketPaths))
	for _, path := range m.VirtioFS.SocketPaths {
		paths = append(paths, m.ResolvePath(path))
	}
	return paths
}

func (m *Manifest) ResolvedLockPath() string {
	return m.ResolvePath(m.Paths.LockPath)
}

func (m *Manifest) ResolvedMicroVMRun() string {
	return m.ResolvePath(m.Paths.MicroVMRun)
}

func (m *Manifest) ResolvedVirtioFSDRun() string {
	return m.ResolvePath(m.Paths.VirtioFSDRun)
}
