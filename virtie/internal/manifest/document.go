package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
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

var defaultSSHArgv = []string{
	"ssh",
	"-q",
	"-o",
	"StrictHostKeyChecking=no",
	"-o",
	"UserKnownHostsFile=/dev/null",
	"-o",
	"GlobalKnownHostsFile=/dev/null",
}

type Document struct {
	Identity      Identity         `json:"identity,omitempty" toml:"identity"`
	Paths         Paths            `json:"paths,omitempty" toml:"paths"`
	Persistence   Persistence      `json:"persistence,omitempty" toml:"persistence"`
	Host          HostFacts        `json:"host,omitempty" toml:"host"`
	QEMU          QEMUFacts        `json:"qemu,omitempty" toml:"qemu"`
	Machine       MachineFacts     `json:"machine,omitempty" toml:"machine"`
	CPU           CPUFacts         `json:"cpu,omitempty" toml:"cpu"`
	Memory        MemoryFacts      `json:"memory,omitempty" toml:"memory"`
	Kernel        KernelFacts      `json:"kernel" toml:"kernel"`
	Graphics      *QEMUGraphics    `json:"graphics,omitempty" toml:"graphics"`
	Sockets       SocketFacts      `json:"sockets,omitempty" toml:"sockets"`
	Volumes       []VolumeFacts    `json:"volumes,omitempty" toml:"volumes"`
	Mounts        []MountFacts     `json:"mounts,omitempty" toml:"mounts"`
	Network       []NetworkFacts   `json:"network,omitempty" toml:"network"`
	Balloon       *balloon.Device  `json:"balloon,omitempty" toml:"balloon"`
	SSH           SSH              `json:"ssh,omitempty" toml:"ssh"`
	VSock         VSock            `json:"vsock,omitempty" toml:"vsock"`
	WriteFiles    []WriteFileFacts `json:"writeFiles,omitempty" toml:"writeFiles"`
	Notifications Notifications    `json:"notifications,omitempty" toml:"notifications"`
}

type HostFacts struct {
	System      string `json:"system,omitempty" toml:"system"`
	OS          string `json:"os,omitempty" toml:"os"`
	Arch        string `json:"arch,omitempty" toml:"arch"`
	NetcatPath  string `json:"netcatPath,omitempty" toml:"netcatPath"`
	QEMUSeccomp bool   `json:"qemuSeccomp,omitempty" toml:"qemuSeccomp"`
}

type QEMUFacts struct {
	BinaryPath string   `json:"binaryPath,omitempty" toml:"binaryPath"`
	User       *string  `json:"user,omitempty" toml:"user"`
	ExtraArgs  []string `json:"extraArgs,omitempty" toml:"extraArgs"`
}

type MachineFacts struct {
	Type    string            `json:"type,omitempty" toml:"type"`
	VCPU    *int              `json:"vcpu,omitempty" toml:"vcpu"`
	ID      *string           `json:"id,omitempty" toml:"id"`
	Options map[string]string `json:"options,omitempty" toml:"options"`
}

type CPUFacts struct {
	Model     string `json:"model,omitempty" toml:"model"`
	EnableKVM *bool  `json:"enableKvm,omitempty" toml:"enableKvm"`
}

type MemoryFacts struct {
	SizeMiB int `json:"sizeMiB,omitempty" toml:"sizeMiB"`
}

type KernelFacts struct {
	Path          string   `json:"path" toml:"path"`
	InitrdPath    string   `json:"initrdPath" toml:"initrdPath"`
	Params        []string `json:"params,omitempty" toml:"params"`
	SerialConsole bool     `json:"serialConsole,omitempty" toml:"serialConsole"`
}

type SocketFacts struct {
	QMP        string `json:"qmp,omitempty" toml:"qmp"`
	GuestAgent string `json:"guestAgent,omitempty" toml:"guestAgent"`
	SSHReady   string `json:"sshReady,omitempty" toml:"sshReady"`
}

type VolumeFacts struct {
	ImagePath  string  `json:"imagePath" toml:"imagePath"`
	SizeMiB    int     `json:"sizeMiB,omitempty" toml:"sizeMiB"`
	FSType     string  `json:"fsType,omitempty" toml:"fsType"`
	AutoCreate bool    `json:"autoCreate,omitempty" toml:"autoCreate"`
	Label      *string `json:"label,omitempty" toml:"label"`
	ReadOnly   bool    `json:"readOnly,omitempty" toml:"readOnly"`
	Direct     *bool   `json:"direct,omitempty" toml:"direct"`
	Serial     *string `json:"serial,omitempty" toml:"serial"`
}

type MountFacts struct {
	Type          string   `json:"type" toml:"type"`
	Tag           string   `json:"tag" toml:"tag"`
	SourcePath    string   `json:"sourcePath,omitempty" toml:"sourcePath"`
	SocketPath    string   `json:"socketPath,omitempty" toml:"socketPath"`
	ReadOnly      bool     `json:"readOnly,omitempty" toml:"readOnly"`
	SecurityModel string   `json:"securityModel,omitempty" toml:"securityModel"`
	Cache         string   `json:"cache,omitempty" toml:"cache"`
	Daemon        *Command `json:"daemon,omitempty" toml:"daemon"`
}

type NetworkFacts struct {
	ID           string        `json:"id,omitempty" toml:"id"`
	Type         string        `json:"type,omitempty" toml:"type"`
	MACAddress   string        `json:"macAddress,omitempty" toml:"macAddress"`
	ForwardPorts []ForwardPort `json:"forwardPorts,omitempty" toml:"forwardPorts"`
}

type ForwardPort struct {
	Proto string       `json:"proto" toml:"proto"`
	From  string       `json:"from" toml:"from"`
	Host  PortEndpoint `json:"host" toml:"host"`
	Guest PortEndpoint `json:"guest" toml:"guest"`
}

type PortEndpoint struct {
	Address string `json:"address" toml:"address"`
	Port    int    `json:"port" toml:"port"`
}

type WriteFileFacts struct {
	GuestPath string  `json:"guestPath" toml:"guestPath"`
	Chown     *string `json:"chown,omitempty" toml:"chown"`
	Text      *string `json:"text,omitempty" toml:"text"`
	Mode      *string `json:"mode,omitempty" toml:"mode"`
	Overwrite *bool   `json:"overwrite,omitempty" toml:"overwrite"`
	Path      *string `json:"path,omitempty" toml:"path"`
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
	doc.Paths.WorkingDir = workingDir
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
		return nil, fmt.Errorf("manifest.kernel.initrdPath is required")
	}

	host := d.Host.withDefaults()
	m := &Manifest{
		Identity:      d.Identity,
		Paths:         d.Paths,
		Persistence:   d.Persistence,
		SSH:           d.SSH,
		VSock:         d.VSock,
		Notifications: d.Notifications,
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
	if m.Paths.LockPath == "" {
		m.Paths.LockPath = filepath.Join(m.Persistence.StateDir, m.Identity.HostName+".lock")
	}
	if m.Paths.RuntimeDir == nil {
		m.Paths.RuntimeDir = stringPtr("")
	}
	if len(m.SSH.Argv) == 0 {
		m.SSH.Argv = append([]string(nil), defaultSSHArgv...)
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
	transport := qemuTransport(machineType, d.Mounts, d.Graphics)
	virtiofsMounts := filterMounts(d.Mounts, "virtiofs")
	memorySize := d.Memory.SizeMiB
	if memorySize == 0 {
		memorySize = defaultMemorySizeMiB
	}
	cpuModel := d.CPU.Model
	if cpuModel == "" {
		cpuModel = defaultCPUModel(host)
	}
	enableKVM := host.OS == "linux" && d.CPU.Model == ""
	if d.CPU.EnableKVM != nil {
		enableKVM = *d.CPU.EnableKVM
	}
	binaryPath := d.QEMU.BinaryPath
	if binaryPath == "" {
		binaryPath = "qemu-system-" + host.Arch
	}
	qmpSocket := d.Sockets.QMP
	if qmpSocket == "" {
		qmpSocket = defaultQMP
	}
	guestAgentSocket := d.Sockets.GuestAgent
	if guestAgentSocket == "" {
		guestAgentSocket = defaultGuestAgent
	}
	sshReadySocket := d.Sockets.SSHReady
	if sshReadySocket == "" {
		sshReadySocket = defaultSSHReadySocket
	}
	noGraphic := d.Graphics == nil

	qemu := QEMU{
		BinaryPath: binaryPath,
		Name:       hostName,
		Machine: QEMUMachine{
			Type:    machineType,
			Options: lowerMachineOptions(host, machineType, d.Machine.Options, transport == "pci"),
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
			SeccompSandbox: host.QEMUSeccomp,
		},
		Graphics: d.Graphics,
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
			Network:  lowerNetwork(d.Network, host, transport, d.Machine.VCPU),
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
		if volume.Direct != nil {
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
		if mount.Type == mountType {
			result = append(result, mount)
		}
	}
	return result
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
		if mount.Type != "virtiofs" || mount.Daemon == nil {
			continue
		}
		daemons = append(daemons, VirtioFSDaemon{
			Tag:        mount.Tag,
			SocketPath: mount.SocketPath,
			Command:    *mount.Daemon,
		})
	}
	return daemons
}

func lowerNetwork(networks []NetworkFacts, host HostFacts, transport string, vcpu *int) []QEMUNetDevice {
	if networks == nil {
		networks = []NetworkFacts{{}}
	}
	devices := make([]QEMUNetDevice, 0, len(networks))
	for _, network := range networks {
		id := network.ID
		if id == "" {
			id = defaultNetworkID
		}
		backend := network.Type
		if backend == "" {
			backend = "user"
		}
		mac := network.MACAddress
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
		devices = append(devices, QEMUNetDevice{
			ID:            id,
			Backend:       backend,
			MacAddress:    mac,
			Transport:     transport,
			RomFile:       romFile,
			NetdevOptions: lowerForwardPorts(network.ForwardPorts, host),
			MQVectors:     mqVectors,
		})
	}
	return devices
}

func lowerForwardPorts(ports []ForwardPort, host HostFacts) []string {
	options := make([]string, 0, len(ports))
	netcat := host.NetcatPath
	if netcat == "" {
		netcat = "nc"
	}
	for _, port := range ports {
		if port.From == "host" {
			options = append(options, fmt.Sprintf("hostfwd=%s:%s:%d-%s:%d", port.Proto, port.Host.Address, port.Host.Port, port.Guest.Address, port.Guest.Port))
		} else {
			options = append(options, fmt.Sprintf("guestfwd=%s:%s:%d-cmd:%s %s %d", port.Proto, port.Guest.Address, port.Guest.Port, netcat, port.Host.Address, port.Host.Port))
		}
	}
	return options
}

func lowerBalloon(device *balloon.Device, transport string) *balloon.Device {
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
			Chown:     file.Chown,
			Text:      file.Text,
			Mode:      file.Mode,
			Overwrite: file.Overwrite,
			Path:      file.Path,
		}
	}
	return result
}

func qemuPassthroughArgs(qemu QEMUFacts) []string {
	args := make([]string, 0, len(qemu.ExtraArgs)+2)
	if qemu.User != nil {
		args = append(args, "-user", *qemu.User)
	}
	args = append(args, qemu.ExtraArgs...)
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
