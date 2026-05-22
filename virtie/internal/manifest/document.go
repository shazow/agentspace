package manifest

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
	HostName      string               `json:"host_name,omitempty" toml:"host_name"`
	WorkingDir    string               `json:"working_dir,omitempty" toml:"working_dir"`
	StateDir      string               `json:"state_dir,omitempty" toml:"state_dir"`
	Host          HostInput            `json:"host,omitempty" toml:"host"`
	QEMU          QEMUInput            `json:"qemu,omitempty" toml:"qemu"`
	Machine       MachineInput         `json:"machine,omitempty" toml:"machine"`
	Kernel        KernelInput          `json:"kernel" toml:"kernel"`
	Graphics      *GraphicsInput       `json:"graphics,omitempty" toml:"graphics"`
	Volumes       []VolumeInput        `json:"volumes,omitempty" toml:"volumes"`
	Mounts        []MountInput         `json:"mounts,omitempty" toml:"mounts"`
	Workspace     WorkspaceInput       `json:"workspace,omitempty" toml:"workspace"`
	Networks      []NetworkInput       `json:"networks,omitempty" toml:"networks"`
	Balloon       *BalloonInput        `json:"balloon,omitempty" toml:"balloon"`
	SSH           SSHInput             `json:"ssh,omitempty" toml:"ssh"`
	VSock         VSockInput           `json:"vsock,omitempty" toml:"vsock"`
	WriteFiles    []WriteFileInput     `json:"write_files,omitempty" toml:"write_files"`
	Notifications NotificationsInput   `json:"notifications,omitempty" toml:"notifications"`
	RunWithTunnel []RunWithTunnelInput `json:"run_with_tunnel,omitempty" toml:"run_with_tunnel"`
}

type HostInput struct {
	OS     string `json:"-" toml:"-"`
	Arch   string `json:"-" toml:"-"`
	System string `json:"-" toml:"-"`
}

type QEMUInput struct {
	Exec             []string          `json:"exec,omitempty" toml:"exec"`
	FwdTunnelExec    []string          `json:"fwd_tunnel_exec,omitempty" toml:"fwd_tunnel_exec"`
	User             *string           `json:"user,omitempty" toml:"user"`
	Seccomp          bool              `json:"seccomp,omitempty" toml:"seccomp"`
	MachineOptions   map[string]string `json:"machine_options,omitempty" toml:"machine_options"`
	QMPSocket        string            `json:"qmp_socket,omitempty" toml:"qmp_socket"`
	GuestAgentSocket string            `json:"guest_agent_socket,omitempty" toml:"guest_agent_socket"`
}

type MachineInput struct {
	Type   string  `json:"type,omitempty" toml:"type"`
	VCPU   *int    `json:"vcpu,omitempty" toml:"vcpu"`
	ID     *string `json:"id,omitempty" toml:"id"`
	Memory int     `json:"memory,omitempty" toml:"memory"`
	CPU    string  `json:"cpu,omitempty" toml:"cpu"`
	KVM    *bool   `json:"kvm,omitempty" toml:"kvm"`
}

type KernelInput struct {
	Path          string   `json:"path" toml:"path"`
	InitrdPath    string   `json:"initrd_path" toml:"initrd_path"`
	Params        []string `json:"params,omitempty" toml:"params"`
	SerialConsole bool     `json:"serial_console,omitempty" toml:"serial_console"`
}

type GraphicsInput struct {
	Backend string `json:"backend,omitempty" toml:"backend"`
}

type VolumeInput struct {
	ImagePath  string  `json:"image" toml:"image"`
	SizeMiB    int     `json:"size,omitempty" toml:"size"`
	FSType     string  `json:"fs,omitempty" toml:"fs"`
	AutoCreate bool    `json:"create,omitempty" toml:"create"`
	Label      *string `json:"label,omitempty" toml:"label"`
	ReadOnly   bool    `json:"read_only,omitempty" toml:"read_only"`
	Direct     bool    `json:"direct,omitempty" toml:"direct"`
	Serial     *string `json:"serial,omitempty" toml:"serial"`
}

type MountInput struct {
	Type          string   `json:"type,omitempty" toml:"type"`
	Tag           string   `json:"tag" toml:"tag"`
	SourcePath    string   `json:"source,omitempty" toml:"source"`
	SocketPath    string   `json:"virtiofsd_socket,omitempty" toml:"virtiofsd_socket"`
	ReadOnly      bool     `json:"read_only,omitempty" toml:"read_only"`
	SecurityModel string   `json:"security_model,omitempty" toml:"security_model"`
	Cache         string   `json:"cache,omitempty" toml:"cache"`
	VirtioFSDExec []string `json:"virtiofsd_exec,omitempty" toml:"virtiofsd_exec"`
}

type WorkspaceInput struct {
	BaseDir  string `json:"basedir,omitempty" toml:"basedir"`
	MountCWD bool   `json:"mount_cwd,omitempty" toml:"mount_cwd"`
}

type NetworkInput struct {
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

type BalloonInput struct {
	Enabled           bool                    `json:"enabled,omitempty" toml:"enabled"`
	DeflateOnOOM      bool                    `json:"deflate_on_oom,omitempty" toml:"deflate_on_oom"`
	FreePageReporting bool                    `json:"free_page_reporting,omitempty" toml:"free_page_reporting"`
	Controller        *BalloonControllerInput `json:"controller,omitempty" toml:"controller"`
}

type BalloonControllerInput struct {
	MinActualMiB             int `json:"min_actual,omitempty" toml:"min_actual"`
	MaxActualMiB             int `json:"max_actual,omitempty" toml:"max_actual"`
	GrowBelowAvailableMiB    int `json:"grow_below_available,omitempty" toml:"grow_below_available"`
	ReclaimAboveAvailableMiB int `json:"reclaim_above_available,omitempty" toml:"reclaim_above_available"`
	StepMiB                  int `json:"step,omitempty" toml:"step"`
	PollIntervalSeconds      int `json:"poll_interval_seconds,omitempty" toml:"poll_interval_seconds"`
	ReclaimHoldoffSeconds    int `json:"reclaim_holdoff_seconds,omitempty" toml:"reclaim_holdoff_seconds"`
}

type SSHInput struct {
	Exec          []string `json:"exec,omitempty" toml:"exec"`
	User          string   `json:"user,omitempty" toml:"user"`
	ReadySocket   string   `json:"ready_socket,omitempty" toml:"ready_socket"`
	RetryDelay    *float64 `json:"retry_delay,omitempty" toml:"retry_delay"`
	Autoprovision bool     `json:"autoprovision,omitempty" toml:"autoprovision"`
}

type VSockInput struct {
	CIDRange RangeInput `json:"cid_range,omitempty" toml:"cid_range"`
}

type RangeInput struct {
	Min int `json:"min,omitempty" toml:"min"`
	Max int `json:"max,omitempty" toml:"max"`
}

type WriteFileInput struct {
	GuestPath   string  `json:"guest_path" toml:"guest_path"`
	Chown       *string `json:"chown,omitempty" toml:"chown"`
	Text        *string `json:"text,omitempty" toml:"text"`
	Mode        *string `json:"mode,omitempty" toml:"mode"`
	Overwrite   *bool   `json:"overwrite,omitempty" toml:"overwrite"`
	FollowLinks *bool   `json:"follow_links,omitempty" toml:"follow_links"`
	WriteBack   *bool   `json:"write_back,omitempty" toml:"write_back"`
	Path        *string `json:"source,omitempty" toml:"source"`
}

type NotificationsInput struct {
	Exec   []string `json:"exec,omitempty" toml:"exec"`
	States []string `json:"states,omitempty" toml:"states"`
}

type RunWithTunnelInput struct {
	SocketPath string            `json:"socket" toml:"socket"`
	Exec       []string          `json:"exec" toml:"exec"`
	Vars       map[string]string `json:"vars,omitempty" toml:"vars"`
}
