package manifest

import (
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	shellquote "github.com/kballard/go-shellquote"
	"github.com/shazow/agentspace/virtie/internal/balloon"
	"github.com/shazow/agentspace/virtie/internal/executor"
)

const virtioFSSocketProbeTimeout = 100 * time.Millisecond

func (d Document) Manifest() (*Manifest, error) {
	return d.ManifestWithOptions(LowerOptions{})
}

func (d Document) ManifestWithOptions(options LowerOptions) (*Manifest, error) {
	if d.Kernel.Path == "" {
		return nil, fmt.Errorf("manifest.kernel.path is required")
	}
	if d.Kernel.InitrdPath == "" {
		return nil, fmt.Errorf("manifest.kernel.initrd_path is required")
	}
	retryDelay, err := lowerRetryDelay(d.SSH.RetryDelay)
	if err != nil {
		return nil, err
	}

	host := d.Host.withDefaults()
	m := &Manifest{
		Identity: Identity{
			HostName: d.HostName,
		},
		Paths: Paths{
			WorkingDir: d.WorkingDir,
		},
		Persistence: Persistence{
			BaseDir:  d.StateDir,
			StateDir: d.StateDir,
		},
		SSH: SSH{
			Argv:          append([]string(nil), d.SSH.Exec...),
			User:          d.SSH.User,
			RetryDelay:    retryDelay,
			Autoprovision: d.SSH.Autoprovision,
		},
		VSock: VSock{
			CIDRange: VSockCIDRange{
				Start: d.VSock.CIDRange.Min,
				End:   d.VSock.CIDRange.Max,
			},
		},
		Notifications: lowerNotifications(d.Notifications),
		Workspace:     lowerWorkspace(d.Workspace),
	}
	if m.Identity.HostName == "" {
		m.Identity.HostName = defaultHostName
	}
	if m.Paths.WorkingDir == "" {
		m.Paths.WorkingDir = defaultWorkingDir
	}
	if m.Persistence.BaseDir == "" {
		m.Persistence.BaseDir = defaultBaseDir
	}
	if m.Persistence.StateDir == "" {
		m.Persistence.StateDir = m.Persistence.BaseDir
	}
	m.Persistence.Directories = persistenceDirectories(d.Volumes, m.Persistence.StateDir)
	if m.Paths.LockPath == "" {
		m.Paths.LockPath = filepath.Join(m.Persistence.StateDir, m.Identity.HostName+".lock")
	}
	m.Paths.RuntimeDir = RuntimeDir{Mode: RuntimeDirPath, Path: m.Persistence.StateDir}
	if m.SSH.User == "" {
		m.SSH.User = defaultSSHUser
	}

	qemu, err := d.lowerQEMU(host, m.Identity.HostName, m.Paths.WorkingDir, m.Persistence.StateDir)
	if err != nil {
		return nil, err
	}
	m.QEMU = qemu
	m.Volumes = lowerVolumes(d.Volumes)
	virtioFSRuns, err := m.lowerVirtioFSRuns(d.Mounts, options)
	if err != nil {
		return nil, err
	}
	m.Run = append(virtioFSRuns, lowerRun(d.Run)...)
	m.WriteFiles = lowerWriteFiles(d.WriteFiles)

	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

func lowerRetryDelay(seconds *float64) (time.Duration, error) {
	if seconds == nil {
		return time.Duration(defaultSSHRetryDelaySeconds * float64(time.Second)), nil
	}
	if math.IsNaN(*seconds) || math.IsInf(*seconds, 0) || *seconds < 0 {
		return 0, fmt.Errorf("manifest.ssh.retry_delay must be a finite number greater than or equal to zero")
	}
	return time.Duration(*seconds * float64(time.Second)), nil
}

func (h HostInput) withDefaults() HostInput {
	if h.OS == "" {
		h.OS = runtime.GOOS
	}
	if h.Arch == "" {
		h.Arch = qemuArch(runtime.GOARCH)
	}
	if h.System == "" {
		h.System = h.Arch + "-" + h.OS
	}
	return h
}

func (d Document) lowerQEMU(host HostInput, hostName string, workingDir string, stateDir string) (QEMU, error) {
	machineType := d.Machine.Type
	if machineType == "" {
		machineType = defaultMachineType
	}
	graphics := lowerGraphics(d.Graphics)
	transport := qemuTransport(machineType, d.Mounts, graphics)
	virtiofsMounts := filterMounts(d.Mounts, "virtiofs")
	memorySize := d.Machine.Memory
	if memorySize == 0 {
		memorySize = defaultMemorySizeMiB
	}
	cpuModel := d.Machine.CPU
	if cpuModel == "" {
		cpuModel = defaultCPUModel(host)
	}
	enableKVM := host.OS == "linux" && d.Machine.CPU == ""
	if d.Machine.KVM != nil {
		enableKVM = *d.Machine.KVM
	}
	qemuRenderer, err := executor.New(executor.Context{
		"HostName":   hostName,
		"WorkingDir": workingDir,
		"StateDir":   stateDir,
		"HostOS":     host.OS,
		"HostArch":   host.Arch,
		"HostSystem": host.System,
	})
	if err != nil {
		return QEMU{}, fmt.Errorf("manifest.qemu.exec: %w", err)
	}
	qemuExec, err := qemuRenderer.RenderArgv(d.QEMU.Exec)
	if err != nil {
		return QEMU{}, fmt.Errorf("manifest.qemu.exec: %w", err)
	}
	binaryPath := ""
	if len(qemuExec) > 0 {
		binaryPath = qemuExec[0]
	}
	if binaryPath == "" {
		binaryPath = "qemu-system-" + host.Arch
	}
	qmpSocket := d.QEMU.QMPSocket
	if qmpSocket == "" {
		qmpSocket = defaultQMP
	}
	guestAgentSocket := d.QEMU.GuestAgentSocket
	if guestAgentSocket == "" {
		guestAgentSocket = defaultGuestAgent
	}
	sshReadySocket := d.SSH.ReadySocket
	if sshReadySocket == "" {
		sshReadySocket = defaultSSHReadySocket
	}
	noGraphic := graphics.IsZero()
	cpus := lowerCPUCount(d.Machine.VCPU)
	networks, err := lowerNetwork(d.Networks, d.QEMU.FwdTunnelExec, host, transport, cpus)
	if err != nil {
		return QEMU{}, err
	}

	qemu := QEMU{
		BinaryPath: binaryPath,
		Name:       hostName,
		Machine: QEMUMachine{
			Type:    machineType,
			Options: lowerMachineOptions(host, machineType, d.QEMU.MachineOptions, transport == "pci"),
		},
		CPU: QEMUCPU{
			Model:     cpuModel,
			EnableKVM: enableKVM,
		},
		Memory: QEMUMemory{
			SizeMiB: memorySize,
			Backend: memoryBackend(host, virtiofsMounts),
			Shared:  len(virtiofsMounts) > 0,
		},
		Kernel: QEMUKernel{
			Path:       d.Kernel.Path,
			InitrdPath: d.Kernel.InitrdPath,
			Params:     kernelParams(host, d.Kernel),
		},
		SMP: QEMUSMP{
			CPUs: cpus,
		},
		Console: QEMUConsole{
			StdioChardev:  true,
			SerialConsole: d.Kernel.SerialConsole,
		},
		Knobs: QEMUKnobs{
			NoDefaults:     true,
			NoUserConfig:   true,
			NoReboot:       true,
			NoGraphic:      noGraphic,
			SeccompSandbox: d.QEMU.Seccomp,
		},
		Graphics: graphics,
		QMP: QEMUQMP{
			SocketPath: qmpSocket,
		},
		GuestAgent: QEMUGuestAgent{
			SocketPath: guestAgentSocket,
		},
		SSHReady: QEMUSSHReady{
			SocketPath: sshReadySocket,
		},
		Devices: QEMUDevices{
			RNG: QEMURNGDevice{
				ID:        "rng0",
				Transport: transport,
			},
			I8042:    host.System == "x86_64-linux",
			Balloon:  lowerBalloon(d.Balloon, transport),
			VirtioFS: lowerVirtioFSMounts(virtiofsMounts, transport),
			NineP:    lowerNinePMounts(filterMounts(d.Mounts, "9p"), transport),
			Block:    lowerBlocks(d.Volumes, host, transport),
			Network:  networks,
			VSOCK: QEMUVSOCKDevice{
				ID:        "vsock0",
				Transport: transport,
			},
		},
		MachineID:       stringValue(d.Machine.ID),
		PassthroughArgs: qemuPassthroughArgs(d.QEMU, qemuExec),
	}
	if d.Machine.Type != "" && d.Machine.Type == machineType {
		// The public schema intentionally keeps machine identity separate
		// from SMBIOS identity for now.
	}
	return qemu, nil
}

func lowerCPUCount(cpus *int) CPUCount {
	if cpus == nil {
		return CPUCount{}
	}
	return ExplicitCPUs(*cpus)
}

func qemuTransport(machineType string, mounts []MountInput, graphics QEMUGraphics) string {
	if !strings.HasPrefix(machineType, "microvm") || len(mounts) > 0 || !graphics.IsZero() {
		return "pci"
	}
	return "mmio"
}

func lowerMachineOptions(host HostInput, machineType string, explicit map[string]string, requirePCI bool) []string {
	options := explicit
	if options == nil {
		options = defaultMachineOptions(host, machineType, requirePCI)
	}
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+options[key])
	}
	return result
}

func defaultMachineOptions(host HostInput, machineType string, requirePCI bool) map[string]string {
	accel := "tcg"
	switch host.OS {
	case "linux":
		accel = "kvm:tcg"
	case "darwin":
		accel = "hvf:tcg"
	}
	switch host.System {
	case "x86_64-linux":
		options := map[string]string{
			"accel":     accel,
			"mem-merge": "on",
			"acpi":      "on",
		}
		if machineType == "microvm" {
			options["pit"] = "off"
			options["pic"] = "off"
			options["pcie"] = boolOnOff(requirePCI)
			options["rtc"] = "on"
			options["usb"] = "off"
		}
		return options
	case "aarch64-linux":
		return map[string]string{"accel": accel, "gic-version": "max"}
	case "aarch64-darwin":
		return map[string]string{"accel": accel}
	default:
		return map[string]string{"accel": accel}
	}
}

func defaultCPUModel(host HostInput) string {
	if host.System == "x86_64-linux" {
		return "host,+x2apic,-sgx"
	}
	return "host"
}

func memoryBackend(host HostInput, virtiofsMounts []MountInput) string {
	if host.OS == "linux" && len(virtiofsMounts) > 0 {
		return "memfd"
	}
	return "default"
}

func kernelParams(host HostInput, kernel KernelInput) string {
	params := make([]string, 0, len(kernel.Params)+3)
	if kernel.SerialConsole {
		switch host.System {
		case "x86_64-linux":
			params = append(params, "earlyprintk=ttyS0 console=ttyS0")
		case "aarch64-linux":
			params = append(params, "console=ttyAMA0")
		}
	}
	params = append(params, "reboot=t", "panic=-1")
	params = append(params, kernel.Params...)
	return strings.Join(params, " ")
}

func lowerVolumes(volumes []VolumeInput) []Volume {
	result := make([]Volume, 0, len(volumes))
	for _, volume := range volumes {
		result = append(result, Volume{
			ImagePath:  volume.ImagePath,
			SizeMiB:    volume.SizeMiB,
			FSType:     volume.FSType,
			AutoCreate: volume.AutoCreate,
			Label:      stringValue(volume.Label),
		})
	}
	return result
}

func lowerBlocks(volumes []VolumeInput, host HostInput, transport string) []QEMUBlockDevice {
	blocks := make([]QEMUBlockDevice, 0, len(volumes))
	for i, volume := range volumes {
		block := QEMUBlockDevice{
			ID:        "vd" + string(rune('a'+i)),
			ImagePath: volume.ImagePath,
			AIO:       aioEngine(host),
			ReadOnly:  volume.ReadOnly,
			Serial:    stringValue(volume.Serial),
			Transport: transport,
		}
		if volume.Direct {
			block.Cache = "none"
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func aioEngine(host HostInput) string {
	if host.OS == "linux" {
		return "io_uring"
	}
	return "threads"
}

func filterMounts(mounts []MountInput, mountType string) []MountInput {
	result := make([]MountInput, 0, len(mounts))
	for _, mount := range mounts {
		if mount.effectiveType() == mountType {
			result = append(result, mount)
		}
	}
	return result
}

func (m MountInput) effectiveType() string {
	if m.Type == "" {
		return "virtiofs"
	}
	return m.Type
}

func lowerWorkspace(workspace WorkspaceInput) Workspace {
	return Workspace{
		GuestDir: workspace.GuestDir,
		HostDir:  workspace.HostDir,
		MountCWD: workspace.MountCWD,
	}
}

func lowerVirtioFSMounts(mounts []MountInput, transport string) []QEMUVirtioFSShare {
	shares := make([]QEMUVirtioFSShare, 0, len(mounts))
	for i, mount := range mounts {
		shares = append(shares, QEMUVirtioFSShare{
			ID:         "fs" + strconv.Itoa(i),
			SocketPath: mount.VirtioFS.Socket,
			Tag:        mount.Tag,
			Transport:  transport,
		})
	}
	return shares
}

func lowerNinePMounts(mounts []MountInput, transport string) []QEMUNinePShare {
	shares := make([]QEMUNinePShare, 0, len(mounts))
	for i, mount := range mounts {
		securityModel := mount.SecurityModel
		if securityModel == "" {
			securityModel = "mapped"
		}
		shares = append(shares, QEMUNinePShare{
			ID:            "fs9p" + strconv.Itoa(i),
			SourcePath:    mount.SourcePath,
			Tag:           mount.Tag,
			SecurityModel: securityModel,
			ReadOnly:      mount.ReadOnly,
			Transport:     transport,
		})
	}
	return shares
}

func (m *Manifest) lowerVirtioFSRuns(mounts []MountInput, options LowerOptions) ([]Run, error) {
	runs := make([]Run, 0, len(mounts))
	for _, mount := range mounts {
		if mount.effectiveType() != "virtiofs" || mount.VirtioFS.Socket == "" {
			continue
		}
		if mount.VirtioFS.Bin == "" && len(mount.VirtioFS.Args) == 0 {
			continue
		}
		bin := mount.VirtioFS.Bin
		if bin == "" {
			bin = "virtiofsd"
		}
		socketPath, err := m.resolveSocketPath(mount.VirtioFS.Socket)
		if err != nil {
			return nil, err
		}
		if info, err := os.Stat(socketPath); err == nil {
			if info.Mode()&os.ModeSocket == 0 {
				if options.Logger != nil {
					options.Logger.Warn("virtiofs socket path exists but is not a socket (possibly leftover from crash); starting virtiofsd anyway", "socket", socketPath)
				}
			} else {
				stale, err := staleUnixSocket(socketPath)
				if stale {
					if options.Logger != nil {
						options.Logger.Warn("virtiofs socket path exists but appears stale; starting virtiofsd anyway", "socket", socketPath, "error", err)
					}
				} else {
					if err != nil && options.Logger != nil {
						options.Logger.Warn("virtiofs socket liveness probe failed; assuming it is externally managed", "socket", socketPath, "error", err)
					} else if options.Logger != nil {
						options.Logger.Info("using existing virtiofs socket", "socket", socketPath)
					}
					continue
				}
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat virtiofs socket %q: %w", socketPath, err)
		}

		args := append([]string(nil), mount.VirtioFS.Args...)
		if len(args) == 0 {
			args = []string{
				"--socket-path={{.Socket}}",
				"--shared-dir={{.MountSource}}",
				"--tag={{.MountTag}}",
			}
		}
		runs = append(runs, Run{
			Name: fmt.Sprintf("virtiofsd[%s]", mount.Tag),
			Exec: append([]string{m.resolvePath(bin)}, args...),
			Env:  []string{"VIRTIOFSD_SOCKET={{.Socket}}"},
			Vars: map[string]any{
				"Socket":      socketPath,
				"MountTag":    mount.Tag,
				"MountSource": m.resolvePath(mount.SourcePath),
			},
		})
		m.addCleanupFile(mount.VirtioFS.Socket)
	}
	return runs, nil
}

func staleUnixSocket(path string) (bool, error) {
	conn, err := net.DialTimeout("unix", path, virtioFSSocketProbeTimeout)
	if err == nil {
		_ = conn.Close()
		return false, nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
		return true, err
	}
	return false, err
}

func (m *Manifest) addCleanupFile(path string) {
	if path == "" {
		return
	}
	for _, existing := range m.CleanupFiles {
		if existing == path {
			return
		}
	}
	m.CleanupFiles = append(m.CleanupFiles, path)
}

func lowerNetwork(networks []NetworkInput, fwdTunnelExec []string, host HostInput, transport string, cpus CPUCount) ([]QEMUNetDevice, error) {
	if networks == nil {
		networks = []NetworkInput{{}}
	}
	devices := make([]QEMUNetDevice, 0, len(networks))
	for i, network := range networks {
		id := network.ID
		if id == "" {
			id = defaultNetworkID
		}
		backend := network.Type
		if backend == "" {
			backend = "user"
		}
		mac := network.MAC
		if mac == "" {
			mac = defaultNetworkMAC
		}
		disableROM := false
		if transport == "pci" || (host.System != "x86_64-linux") {
			disableROM = true
		}
		mqVectors := 0
		if cpus.Set && cpus.Value > 1 && transport == "pci" {
			mqVectors = 2*cpus.Value + 2
		}
		forwardOptions, err := lowerForwardPorts(network.Forward, fwdTunnelExec, i)
		if err != nil {
			return nil, err
		}
		devices = append(devices, QEMUNetDevice{
			ID:            id,
			Backend:       backend,
			MacAddress:    mac,
			Transport:     transport,
			DisableROM:    disableROM,
			NetdevOptions: forwardOptions,
			MQVectors:     mqVectors,
		})
	}
	return devices, nil
}

func parsePortEndpoint(value string) (PortEndpoint, error) {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		index := strings.LastIndex(value, ":")
		if index < 0 {
			return PortEndpoint{}, fmt.Errorf("missing :port")
		}
		host = value[:index]
		port = value[index+1:]
	}
	if port == "" {
		return PortEndpoint{}, fmt.Errorf("missing port")
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil {
		return PortEndpoint{}, fmt.Errorf("port must be an integer")
	}
	if parsedPort <= 0 || parsedPort > 65535 {
		return PortEndpoint{}, fmt.Errorf("port must be between 1 and 65535")
	}
	return PortEndpoint{Address: host, Port: parsedPort}, nil
}

func lowerForwardPorts(ports []ForwardPort, fwdTunnelExec []string, networkIndex int) ([]string, error) {
	options := make([]string, 0, len(ports))
	if len(fwdTunnelExec) == 0 {
		fwdTunnelExec = []string{"nc", "{{.Host}}", "{{.Port}}"}
	}
	for i, port := range ports {
		proto := port.Proto
		if proto == "" {
			proto = "tcp"
		}
		if proto != "tcp" && proto != "udp" {
			return nil, fmt.Errorf("manifest.networks[%d].forward[%d].proto must be one of tcp or udp", networkIndex, i)
		}
		from := port.From
		if from == "" {
			from = "host"
		}
		if from != "host" && from != "guest" {
			return nil, fmt.Errorf("manifest.networks[%d].forward[%d].from must be one of host or guest", networkIndex, i)
		}
		hostEndpoint, err := parsePortEndpoint(port.Host)
		if err != nil {
			return nil, fmt.Errorf("manifest.networks[%d].forward[%d].host %s", networkIndex, i, err)
		}
		guestEndpoint, err := parsePortEndpoint(port.Guest)
		if err != nil {
			return nil, fmt.Errorf("manifest.networks[%d].forward[%d].guest %s", networkIndex, i, err)
		}
		if from == "host" {
			options = append(options, fmt.Sprintf("hostfwd=%s:%s:%d-%s:%d", proto, hostEndpoint.Address, hostEndpoint.Port, guestEndpoint.Address, guestEndpoint.Port))
		} else {
			if err := rejectLegacyFwdTunnelExecEnv(fwdTunnelExec); err != nil {
				return nil, fmt.Errorf("manifest.networks[%d].forward[%d].fwd_tunnel_exec: %w", networkIndex, i, err)
			}
			command, err := renderFwdTunnelExec(fwdTunnelExec, hostEndpoint)
			if err != nil {
				return nil, fmt.Errorf("manifest.networks[%d].forward[%d].fwd_tunnel_exec: %w", networkIndex, i, err)
			}
			options = append(options, fmt.Sprintf("guestfwd=%s:%s:%d-cmd:%s", proto, guestEndpoint.Address, guestEndpoint.Port, shellquote.Join(command...)))
		}
	}
	return options, nil
}

func rejectLegacyFwdTunnelExecEnv(exec []string) error {
	for i, arg := range exec {
		switch arg {
		case "$HOST":
			return fmt.Errorf("exec[%d] uses legacy $HOST; use {{.Host}}", i)
		case "$PORT":
			return fmt.Errorf("exec[%d] uses legacy $PORT; use {{.Port}}", i)
		}
	}
	return nil
}

func renderFwdTunnelExec(exec []string, hostEndpoint PortEndpoint) ([]string, error) {
	renderer, err := executor.New(executor.Context{
		"Host": hostEndpoint.Address,
		"Port": strconv.Itoa(hostEndpoint.Port),
	})
	if err != nil {
		return nil, err
	}
	command, err := renderer.RenderArgv(exec)
	if err != nil {
		return nil, err
	}
	return command, nil
}

func lowerBalloon(facts *BalloonInput, transport string) *balloon.Device {
	if facts == nil || !facts.Enabled {
		return nil
	}
	device := &balloon.Device{
		DeflateOnOOM:      facts.DeflateOnOOM,
		FreePageReporting: facts.FreePageReporting,
	}
	if facts.Controller != nil {
		device.Controller = &balloon.ControllerConfig{
			MinActualMiB:             facts.Controller.MinActualMiB,
			MaxActualMiB:             facts.Controller.MaxActualMiB,
			GrowBelowAvailableMiB:    facts.Controller.GrowBelowAvailableMiB,
			ReclaimAboveAvailableMiB: facts.Controller.ReclaimAboveAvailableMiB,
			StepMiB:                  facts.Controller.StepMiB,
			PollIntervalSeconds:      facts.Controller.PollIntervalSeconds,
			ReclaimHoldoffSeconds:    facts.Controller.ReclaimHoldoffSeconds,
		}
	}
	if device == nil {
		return nil
	}
	copy := *device
	if copy.ID == "" {
		copy.ID = "balloon0"
	}
	if copy.Transport == "" {
		copy.Transport = transport
	}
	if !copy.FreePageReporting {
		copy.FreePageReporting = true
	}
	return &copy
}

func lowerWriteFiles(files []WriteFileInput) WriteFiles {
	result := make(WriteFiles, len(files))
	for _, file := range files {
		result[file.GuestPath] = WriteFile{
			Chown:       stringValue(file.Chown),
			Mode:        stringValue(file.Mode),
			Overwrite:   boolValue(file.Overwrite),
			FollowLinks: boolValueDefault(file.FollowLinks, true),
			WriteBack:   boolValue(file.WriteBack),
			Content:     lowerWriteFileContent(file),
		}
	}
	return result
}

func lowerWriteFileContent(file WriteFileInput) WriteFileContent {
	switch {
	case file.Text != nil && file.Path != nil:
		return WriteFileContent{Kind: WriteFileContentNone, Text: *file.Text, Path: *file.Path}
	case file.Text != nil:
		return WriteFileContent{Kind: WriteFileContentText, Text: *file.Text}
	case file.Path != nil:
		return WriteFileContent{Kind: WriteFileContentPath, Path: *file.Path}
	default:
		return WriteFileContent{}
	}
}

func lowerGraphics(graphics *GraphicsInput) QEMUGraphics {
	if graphics == nil || graphics.Backend == "" || graphics.Backend == "headless" {
		return QEMUGraphics{}
	}
	return QEMUGraphics{Backend: graphics.Backend}
}

func lowerNotifications(notifications NotificationsInput) Notifications {
	result := Notifications{
		States: append([]string(nil), notifications.States...),
	}
	if len(notifications.Exec) > 0 {
		command := commandFromExec(notifications.Exec)
		result.Command = command
	}
	return result
}

func lowerRun(runs []RunInput) []Run {
	result := make([]Run, 0, len(runs))
	for _, run := range runs {
		result = append(result, Run{
			Exec: append([]string(nil), run.Exec...),
			Vars: cloneValueMap(run.Vars),
		})
	}
	return result
}

func commandFromExec(exec []string) Command {
	if len(exec) == 0 {
		return Command{}
	}
	return Command{
		Path: exec[0],
		Args: append([]string(nil), exec[1:]...),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func cloneValueMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]any, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func persistenceDirectories(volumes []VolumeInput, stateDir string) []string {
	dirs := []string{stateDir}
	for _, volume := range volumes {
		if volume.ImagePath == "" {
			continue
		}
		dir := filepath.Dir(volume.ImagePath)
		if dir == "." {
			continue
		}
		dirs = append(dirs, dir)
	}
	return uniqueStrings(dirs)
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func qemuPassthroughArgs(qemu QEMUInput, exec []string) []string {
	extraArgs := []string(nil)
	if len(exec) > 1 {
		extraArgs = exec[1:]
	}
	args := make([]string, 0, len(extraArgs)+2)
	if qemu.User != nil && *qemu.User != "" {
		args = append(args, "-user", *qemu.User)
	}
	args = append(args, extraArgs...)
	return args
}

func qemuArch(goArch string) string {
	switch goArch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	default:
		return goArch
	}
}

func boolOnOff(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func boolValue(value *bool) bool {
	return value != nil && *value
}

func boolValueDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
