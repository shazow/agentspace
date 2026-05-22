package manifest

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adrg/xdg"
	"github.com/shazow/agentspace/virtie/internal/executor"
)

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

func (m *Manifest) ResolvedTunnelDir() string {
	return filepath.Join(m.ResolvedPersistenceStateDir(), "tunnels")
}

func (m *Manifest) ResolvedRunWithTunnelSocketPaths() []string {
	paths := make([]string, 0, len(m.RunWithTunnel))
	for _, tunnel := range m.RunWithTunnel {
		paths = append(paths, filepath.Join(m.ResolvedTunnelDir(), tunnel.SocketPath))
	}
	return paths
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
		renderer, err := executor.New(executor.Context{
			"Socket": socketPath,
			"Tag":    daemon.Tag,
		})
		if err != nil {
			return nil, fmt.Errorf("manifest.virtiofs.daemons[%s].command: %w", daemon.Tag, err)
		}
		command, err := RenderCommand(daemon.Command, renderer)
		if err != nil {
			return nil, fmt.Errorf("manifest.virtiofs.daemons[%s].command: %w", daemon.Tag, err)
		}
		resolved.Command = Command{
			Path: m.resolvePath(command.Path),
			Args: command.Args,
			Env:  command.Env,
		}
		daemons = append(daemons, resolved)
	}
	return daemons, nil
}

func (m *Manifest) ResolvedRunWithTunnels() ([]ResolvedRunWithTunnel, error) {
	tunnels := make([]ResolvedRunWithTunnel, 0, len(m.RunWithTunnel))
	tunnelDir := m.ResolvedTunnelDir()
	for i, tunnel := range m.RunWithTunnel {
		socketPath := filepath.Join(tunnelDir, tunnel.SocketPath)
		guestSocketPath := filepath.Join("/run/tunnels", tunnel.SocketPath)
		context := executor.Context{
			"Socket":      socketPath,
			"GuestSocket": guestSocketPath,
		}
		for key, value := range tunnel.Vars {
			context[key] = value
		}
		renderer, err := executor.New(context)
		if err != nil {
			return nil, fmt.Errorf("manifest.runWithTunnel[%d].exec: %w", i, err)
		}
		exec, err := renderer.RenderArgv(tunnel.Exec)
		if err != nil {
			return nil, fmt.Errorf("manifest.runWithTunnel[%d].exec: %w", i, err)
		}
		env := append([]string(nil), tunnel.Env...)
		env = append(env, renderer.Env()...)
		tunnels = append(tunnels, ResolvedRunWithTunnel{
			SocketPath:      socketPath,
			GuestSocketPath: guestSocketPath,
			Exec:            exec,
			Env:             env,
			Dir:             tunnelDir,
			Vars:            cloneStringMap(tunnel.Vars),
		})
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
		command.Env = append([]string(nil), m.Notifications.Command.Env...)
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
