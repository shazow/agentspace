package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	shellquote "github.com/kballard/go-shellquote"
	"github.com/shazow/agentspace/virtie/internal/balloon"
)

const (
	defaultHostName      = "virtie"
	defaultWorkingDir    = "."
	defaultBaseDir       = ".virtie"
	defaultMachineType   = "microvm"
	defaultMemorySizeMiB = 1024
	defaultQMP           = "qmp.sock"
	defaultGuestAgent    = "qga.sock"
	defaultSSHUser       = "agent"
	defaultNetworkID     = "microvm1"
	defaultNetworkMAC    = "02:02:00:00:00:01"
)

type Document struct {
	HostName      string             `json:"host_name,omitempty" toml:"host_name"`
	WorkingDir    string             `json:"working_dir,omitempty" toml:"working_dir"`
	StateDir      string             `json:"state_dir,omitempty" toml:"state_dir"`
	Host          HostFacts          `json:"host,omitempty" toml:"host"`
	QEMU          QEMUFacts          `json:"qemu,omitempty" toml:"qemu"`
	Machine       MachineFacts       `json:"machine,omitempty" toml:"machine"`
	Kernel        KernelFacts        `json:"kernel" toml:"kernel"`
	Graphics      *GraphicsFacts     `json:"graphics,omitempty" toml:"graphics"`
	Volumes       []VolumeFacts      `json:"volumes,omitempty" toml:"volumes"`
	Mounts        []MountFacts       `json:"mounts,omitempty" toml:"mounts"`
	Workspace     WorkspaceFacts     `json:"workspace,omitempty" toml:"workspace"`
	Networks      []NetworkFacts     `json:"networks,omitempty" toml:"networks"`
	Balloon       *BalloonFacts      `json:"balloon,omitempty" toml:"balloon"`
	SSH           SSHFacts           `json:"ssh,omitempty" toml:"ssh"`
	VSock         VSockFacts         `json:"vsock,omitempty" toml:"vsock"`
	WriteFiles    []WriteFileFacts   `json:"write_files,omitempty" toml:"write_files"`
	Notifications NotificationsFacts `json:"notifications,omitempty" toml:"notifications"`
}

type HostFacts struct {
	OS     string `json:"-" toml:"-"`
	Arch   string `json:"-" toml:"-"`
	System string `json:"-" toml:"-"`
}

type QEMUFacts struct {
	Exec             []string          `json:"exec,omitempty" toml:"exec"`
	FwdTunnelExec    []string          `json:"fwd_tunnel_exec,omitempty" toml:"fwd_tunnel_exec"`
	User             *string           `json:"user,omitempty" toml:"user"`
	Seccomp          bool              `json:"seccomp,omitempty" toml:"seccomp"`
	MachineOptions   map[string]string `json:"machine_options,omitempty" toml:"machine_options"`
	QMPSocket        string            `json:"qmp_socket,omitempty" toml:"qmp_socket"`
	GuestAgentSocket string            `json:"guest_agent_socket,omitempty" toml:"guest_agent_socket"`
}

type MachineFacts struct {
	Type   string  `json:"type,omitempty" toml:"type"`
	VCPU   *int    `json:"vcpu,omitempty" toml:"vcpu"`
	ID     *string `json:"id,omitempty" toml:"id"`
	Memory int     `json:"memory,omitempty" toml:"memory"`
	CPU    string  `json:"cpu,omitempty" toml:"cpu"`
	KVM    *bool   `json:"kvm,omitempty" toml:"kvm"`
}

type KernelFacts struct {
	Path          string   `json:"path" toml:"path"`
	InitrdPath    string   `json:"initrd_path" toml:"initrd_path"`
	Params        []string `json:"params,omitempty" toml:"params"`
	SerialConsole bool     `json:"serial_console,omitempty" toml:"serial_console"`
}

type GraphicsFacts struct {
	Backend string `json:"backend,omitempty" toml:"backend"`
}

type VolumeFacts struct {
	ImagePath  string  `json:"image" toml:"image"`
	SizeMiB    int     `json:"size,omitempty" toml:"size"`
	FSType     string  `json:"fs,omitempty" toml:"fs"`
	AutoCreate bool    `json:"create,omitempty" toml:"create"`
	Label      *string `json:"label,omitempty" toml:"label"`
	ReadOnly   bool    `json:"read_only,omitempty" toml:"read_only"`
	Direct     bool    `json:"direct,omitempty" toml:"direct"`
	Serial     *string `json:"serial,omitempty" toml:"serial"`
}

type MountFacts struct {
	Type          string   `json:"type,omitempty" toml:"type"`
	Tag           string   `json:"tag" toml:"tag"`
	SourcePath    string   `json:"source,omitempty" toml:"source"`
	SocketPath    string   `json:"virtiofsd_socket,omitempty" toml:"virtiofsd_socket"`
	ReadOnly      bool     `json:"read_only,omitempty" toml:"read_only"`
	SecurityModel string   `json:"security_model,omitempty" toml:"security_model"`
	Cache         string   `json:"cache,omitempty" toml:"cache"`
	VirtioFSDExec []string `json:"virtiofsd_exec,omitempty" toml:"virtiofsd_exec"`
}

type WorkspaceFacts struct {
	BaseDir  string `json:"basedir,omitempty" toml:"basedir"`
	MountCWD bool   `json:"mount_cwd,omitempty" toml:"mount_cwd"`
}

type NetworkFacts struct {
	ID      string        `json:"id,omitempty" toml:"id"`
	Type    string        `json:"type,omitempty" toml:"type"`
	MAC     string        `json:"mac,omitempty" toml:"mac"`
	Forward []ForwardPort `json:"forward,omitempty" toml:"forward"`
}

type ForwardPort struct {
	Proto string `json:"proto" toml:"proto"`
	From  string `json:"from" toml:"from"`
	Host  string `json:"host" toml:"host"`
	Guest string `json:"guest" toml:"guest"`
}

type PortEndpoint struct {
	Address string `json:"address" toml:"address"`
	Port    int    `json:"port" toml:"port"`
}

type BalloonFacts struct {
	Enabled           bool                    `json:"enabled,omitempty" toml:"enabled"`
	DeflateOnOOM      bool                    `json:"deflate_on_oom,omitempty" toml:"deflate_on_oom"`
	FreePageReporting bool                    `json:"free_page_reporting,omitempty" toml:"free_page_reporting"`
	Controller        *BalloonControllerFacts `json:"controller,omitempty" toml:"controller"`
}

type BalloonControllerFacts struct {
	MinActualMiB             int `json:"min_actual,omitempty" toml:"min_actual"`
	MaxActualMiB             int `json:"max_actual,omitempty" toml:"max_actual"`
	GrowBelowAvailableMiB    int `json:"grow_below_available,omitempty" toml:"grow_below_available"`
	ReclaimAboveAvailableMiB int `json:"reclaim_above_available,omitempty" toml:"reclaim_above_available"`
	StepMiB                  int `json:"step,omitempty" toml:"step"`
	PollIntervalSeconds      int `json:"poll_interval_seconds,omitempty" toml:"poll_interval_seconds"`
	ReclaimHoldoffSeconds    int `json:"reclaim_holdoff_seconds,omitempty" toml:"reclaim_holdoff_seconds"`
}

type SSHFacts struct {
	Exec          []string `json:"exec,omitempty" toml:"exec"`
	User          string   `json:"user,omitempty" toml:"user"`
	ReadySocket   string   `json:"ready_socket,omitempty" toml:"ready_socket"`
	RetryDelay    *float64 `json:"retry_delay,omitempty" toml:"retry_delay"`
	Autoprovision bool     `json:"autoprovision,omitempty" toml:"autoprovision"`
}

type VSockFacts struct {
	CIDRange RangeFacts `json:"cid_range,omitempty" toml:"cid_range"`
}

type RangeFacts struct {
	Min int `json:"min,omitempty" toml:"min"`
	Max int `json:"max,omitempty" toml:"max"`
}

type WriteFileFacts struct {
	GuestPath   string  `json:"guest_path" toml:"guest_path"`
	Chown       *string `json:"chown,omitempty" toml:"chown"`
	Text        *string `json:"text,omitempty" toml:"text"`
	Mode        *string `json:"mode,omitempty" toml:"mode"`
	Overwrite   *bool   `json:"overwrite,omitempty" toml:"overwrite"`
	FollowLinks *bool   `json:"follow_links,omitempty" toml:"follow_links"`
	WriteBack   *bool   `json:"write_back,omitempty" toml:"write_back"`
	Path        *string `json:"source,omitempty" toml:"source"`
}

type NotificationsFacts struct {
	Exec   []string `json:"exec,omitempty" toml:"exec"`
	States []string `json:"states,omitempty" toml:"states"`
}

func LoadBytes(data []byte, name string) (*Manifest, error) {
	var doc Document
	var err error
	if manifestLooksTOML(data, name) {
		err = decodeTOML(data, &doc)
	} else {
		err = decodeJSON(data, &doc)
	}
	if err != nil {
		return nil, err
	}
	return doc.Manifest()
}

func UpdateWorkingDirBytes(data []byte, name string, workingDir string) ([]byte, error) {
	var doc Document
	isTOML := manifestLooksTOML(data, name)
	var err error
	if isTOML {
		err = decodeTOML(data, &doc)
	} else {
		err = decodeJSON(data, &doc)
	}
	if err != nil {
		return nil, err
	}
	doc.WorkingDir = workingDir
	if isTOML {
		var out bytes.Buffer
		if err := toml.NewEncoder(&out).Encode(doc); err != nil {
			return nil, fmt.Errorf("encode manifest: %w", err)
		}
		return out.Bytes(), nil
	}
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	return append(updated, '\n'), nil
}

func decodeJSON(data []byte, doc *Document) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(doc); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != nil {
		if err.Error() == "EOF" {
			return nil
		}
		return fmt.Errorf("decode manifest: %w", err)
	}
	return fmt.Errorf("decode manifest: unexpected trailing data")
}

func decodeTOML(data []byte, doc *Document) error {
	metadata, err := toml.NewDecoder(bytes.NewReader(data)).Decode(doc)
	if err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		return fmt.Errorf("decode manifest: unknown key %s", undecoded[0].String())
	}
	return nil
}

func manifestLooksTOML(data []byte, name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".toml":
		return true
	case ".json":
		return false
	}
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] != '{'
}

func (d Document) Manifest() (*Manifest, error) {
	if d.Kernel.Path == "" {
		return nil, fmt.Errorf("manifest.kernel.path is required")
	}
	if d.Kernel.InitrdPath == "" {
		return nil, fmt.Errorf("manifest.kernel.initrd_path is required")
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
			RetryDelay:    d.SSH.RetryDelay,
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
	if m.Paths.RuntimeDir == nil {
		m.Paths.RuntimeDir = stringPtr(m.Persistence.StateDir)
	}
	if m.SSH.User == "" {
		m.SSH.User = defaultSSHUser
	}

	qemu, err := d.lowerQEMU(host, m.Identity.HostName)
	if err != nil {
		return nil, err
	}
	m.QEMU = qemu
	m.Volumes = lowerVolumes(d.Volumes)
	m.VirtioFS.Daemons = lowerVirtioFSDaemons(d.Mounts)
	m.WriteFiles = lowerWriteFiles(d.WriteFiles)

	if err := m.Validate(); err != nil {
		return nil, err
	}
	return m, nil
}

func (h HostFacts) withDefaults() HostFacts {
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

func (d Document) lowerQEMU(host HostFacts, hostName string) (QEMU, error) {
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
	binaryPath := ""
	if len(d.QEMU.Exec) > 0 {
		binaryPath = d.QEMU.Exec[0]
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
	noGraphic := graphics == nil
	networks, err := lowerNetwork(d.Networks, d.QEMU.FwdTunnelExec, host, transport, d.Machine.VCPU)
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
			CPUs: d.Machine.VCPU,
		},
		Console: QEMUConsole{
			StdioChardev:  true,
			SerialConsole: d.Kernel.SerialConsole,
		},
		Knobs: QEMUKnobs{
			NoDefaults:     true,
			NoUserConfig:   true,
			NoReboot:       true,
			NoGraphic:      &noGraphic,
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
		MachineID:       d.Machine.ID,
		PassthroughArgs: qemuPassthroughArgs(d.QEMU),
	}
	if d.Machine.Type != "" && d.Machine.Type == machineType {
		// The public schema intentionally keeps machine identity separate
		// from SMBIOS identity for now.
	}
	return qemu, nil
}

func qemuTransport(machineType string, mounts []MountFacts, graphics *QEMUGraphics) string {
	if !strings.HasPrefix(machineType, "microvm") || len(mounts) > 0 || graphics != nil {
		return "pci"
	}
	return "mmio"
}

func lowerMachineOptions(host HostFacts, machineType string, explicit map[string]string, requirePCI bool) []string {
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

func defaultMachineOptions(host HostFacts, machineType string, requirePCI bool) map[string]string {
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

func defaultCPUModel(host HostFacts) string {
	if host.System == "x86_64-linux" {
		return "host,+x2apic,-sgx"
	}
	return "host"
}

func memoryBackend(host HostFacts, virtiofsMounts []MountFacts) string {
	if host.OS == "linux" && len(virtiofsMounts) > 0 {
		return "memfd"
	}
	return "default"
}

func kernelParams(host HostFacts, kernel KernelFacts) string {
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

func lowerVolumes(volumes []VolumeFacts) []Volume {
	result := make([]Volume, 0, len(volumes))
	for _, volume := range volumes {
		result = append(result, Volume{
			ImagePath:  volume.ImagePath,
			SizeMiB:    volume.SizeMiB,
			FSType:     volume.FSType,
			AutoCreate: volume.AutoCreate,
			Label:      volume.Label,
		})
	}
	return result
}

func lowerBlocks(volumes []VolumeFacts, host HostFacts, transport string) []QEMUBlockDevice {
	blocks := make([]QEMUBlockDevice, 0, len(volumes))
	for i, volume := range volumes {
		block := QEMUBlockDevice{
			ID:        "vd" + string(rune('a'+i)),
			ImagePath: volume.ImagePath,
			AIO:       aioEngine(host),
			ReadOnly:  volume.ReadOnly,
			Serial:    volume.Serial,
			Transport: transport,
		}
		if volume.Direct {
			cache := "none"
			block.Cache = &cache
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func aioEngine(host HostFacts) string {
	if host.OS == "linux" {
		return "io_uring"
	}
	return "threads"
}

func filterMounts(mounts []MountFacts, mountType string) []MountFacts {
	result := make([]MountFacts, 0, len(mounts))
	for _, mount := range mounts {
		if mount.effectiveType() == mountType {
			result = append(result, mount)
		}
	}
	return result
}

func (m MountFacts) effectiveType() string {
	if m.Type == "" {
		return "virtiofs"
	}
	return m.Type
}

func lowerWorkspace(workspace WorkspaceFacts) Workspace {
	return Workspace{
		BaseDir:  workspace.BaseDir,
		MountCWD: workspace.MountCWD,
	}
}

func lowerVirtioFSMounts(mounts []MountFacts, transport string) []QEMUVirtioFSShare {
	shares := make([]QEMUVirtioFSShare, 0, len(mounts))
	for i, mount := range mounts {
		shares = append(shares, QEMUVirtioFSShare{
			ID:         "fs" + strconv.Itoa(i),
			SocketPath: mount.SocketPath,
			Tag:        mount.Tag,
			Transport:  transport,
		})
	}
	return shares
}

func lowerNinePMounts(mounts []MountFacts, transport string) []QEMUNinePShare {
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

func lowerVirtioFSDaemons(mounts []MountFacts) []VirtioFSDaemon {
	daemons := make([]VirtioFSDaemon, 0, len(mounts))
	for _, mount := range mounts {
		if mount.effectiveType() != "virtiofs" || len(mount.VirtioFSDExec) == 0 {
			continue
		}
		command := commandFromExec(mount.VirtioFSDExec)
		daemons = append(daemons, VirtioFSDaemon{
			Tag:        mount.Tag,
			SocketPath: mount.SocketPath,
			Command:    command,
		})
	}
	return daemons
}

func lowerNetwork(networks []NetworkFacts, fwdTunnelExec []string, host HostFacts, transport string, vcpu *int) ([]QEMUNetDevice, error) {
	if networks == nil {
		networks = []NetworkFacts{{}}
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
		var romFile *string
		if transport == "pci" || (host.System != "x86_64-linux") {
			empty := ""
			romFile = &empty
		}
		mqVectors := 0
		if vcpu != nil && *vcpu > 1 && transport == "pci" {
			mqVectors = 2**vcpu + 2
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
			RomFile:       romFile,
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
		fwdTunnelExec = []string{"nc", "$HOST", "$PORT"}
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
			command := expandFwdTunnelExec(fwdTunnelExec, hostEndpoint)
			options = append(options, fmt.Sprintf("guestfwd=%s:%s:%d-cmd:%s", proto, guestEndpoint.Address, guestEndpoint.Port, shellquote.Join(command...)))
		}
	}
	return options, nil
}

func expandFwdTunnelExec(exec []string, hostEndpoint PortEndpoint) []string {
	result := make([]string, 0, len(exec))
	port := strconv.Itoa(hostEndpoint.Port)
	for _, arg := range exec {
		arg = strings.ReplaceAll(arg, "$HOST", hostEndpoint.Address)
		arg = strings.ReplaceAll(arg, "$PORT", port)
		result = append(result, arg)
	}
	return result
}

func lowerBalloon(facts *BalloonFacts, transport string) *balloon.Device {
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

func lowerWriteFiles(files []WriteFileFacts) WriteFiles {
	result := make(WriteFiles, len(files))
	for _, file := range files {
		result[file.GuestPath] = WriteFile{
			Chown:       file.Chown,
			Text:        file.Text,
			Mode:        file.Mode,
			Overwrite:   file.Overwrite,
			FollowLinks: file.FollowLinks,
			WriteBack:   file.WriteBack,
			Path:        file.Path,
		}
	}
	return result
}

func lowerGraphics(graphics *GraphicsFacts) *QEMUGraphics {
	if graphics == nil || graphics.Backend == "" || graphics.Backend == "headless" {
		return nil
	}
	return &QEMUGraphics{Backend: graphics.Backend}
}

func lowerNotifications(notifications NotificationsFacts) Notifications {
	result := Notifications{
		States: append([]string(nil), notifications.States...),
	}
	if len(notifications.Exec) > 0 {
		command := commandFromExec(notifications.Exec)
		result.Command = &command
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

func persistenceDirectories(volumes []VolumeFacts, stateDir string) []string {
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

func qemuPassthroughArgs(qemu QEMUFacts) []string {
	extraArgs := []string(nil)
	if len(qemu.Exec) > 1 {
		extraArgs = qemu.Exec[1:]
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
