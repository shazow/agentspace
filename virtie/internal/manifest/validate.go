package manifest

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
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

func (m *Manifest) applyDefaults() {
	if m == nil {
		return
	}

	if m.QEMU.SSHReady.SocketPath == "" {
		m.QEMU.SSHReady.SocketPath = defaultSSHReadySocket
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
	case m.SSH.RetryDelay < 0:
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

	if !m.QEMU.Graphics.IsZero() && !validQEMUGraphicsBackend(m.QEMU.Graphics.Backend) {
		return fmt.Errorf("manifest.qemu.graphics.backend must be one of gtk or cocoa")
	}

	for i, run := range m.Run {
		if err := validateRun(i, run); err != nil {
			return err
		}
	}
	for i, path := range m.CleanupFiles {
		if path == "" {
			return fmt.Errorf("manifest.cleanupFiles[%d] must not be empty", i)
		}
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

func validateRun(index int, run Run) error {
	switch {
	case len(run.Exec) == 0:
		return fmt.Errorf("manifest.run[%d].exec is required", index)
	case run.Exec[0] == "":
		return fmt.Errorf("manifest.run[%d].exec[0] is required", index)
	}
	for key := range run.Vars {
		if key == "CID" || key == "StateDir" || key == "Workspace" || key == "Env" {
			return fmt.Errorf("manifest.run[%d].vars key %q is reserved", index, key)
		}
	}
	context := executor.Context{
		"CID":       "3",
		"StateDir":  ".virtie",
		"Workspace": "/workspace",
	}
	for key, value := range run.Vars {
		context[key] = value
	}
	if _, err := executor.New(context); err != nil {
		return fmt.Errorf("manifest.run[%d].vars: %w", index, err)
	}
	return nil
}

func pathEscapesBase(path string) bool {
	cleaned := filepath.Clean(path)
	return cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

func (m *Manifest) SSHRetryDelay(fallback time.Duration) time.Duration {
	if m == nil {
		return fallback
	}
	return m.SSH.RetryDelay
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
		case entry.Content.Kind == WriteFileContentNone:
			return fmt.Errorf("manifest.writeFiles[%q] must set exactly one of text or path", guestPath)
		case entry.Content.Kind != WriteFileContentText && entry.Content.Kind != WriteFileContentPath:
			return fmt.Errorf("manifest.writeFiles[%q] must set exactly one of text or path", guestPath)
		case entry.Content.Kind == WriteFileContentPath && entry.Content.Path == "":
			return fmt.Errorf("manifest.writeFiles[%q].path must not be empty", guestPath)
		case entry.WriteBack && entry.Content.Kind != WriteFileContentPath:
			return fmt.Errorf("manifest.writeFiles[%q].writeBack requires path", guestPath)
		case entry.Mode != "" && !writeFileModePattern.MatchString(entry.Mode):
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
