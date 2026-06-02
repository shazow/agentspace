package manifest

import (
	"github.com/shazow/agentspace/virtie/internal/manifest/tagged"
	"github.com/shazow/agentspace/virtie/internal/units"
)

const (
	KernelSerialOff     = "off"
	KernelSerialPrint   = "print"
	KernelSerialConsole = "console"

	defaultHostName    = "virtie"
	defaultWorkingDir  = "."
	defaultBaseDir     = ".virtie"
	defaultMachineType = "microvm"
	defaultMemorySize  = units.MiB(1024)
	defaultQMP         = "qmp.sock"
	defaultGuestAgent  = "qga.sock"
	defaultSSHUser     = "agent"
	defaultNetworkID   = "microvm1"
	defaultNetworkMAC  = "02:02:00:00:00:01"
)

type Document struct {
	HostName      string             `json:"host_name,omitempty" toml:"host_name"`
	WorkingDir    string             `json:"working_dir,omitempty" toml:"working_dir"`
	StateDir      string             `json:"state_dir,omitempty" toml:"state_dir"`
	Host          HostInput          `json:"host,omitempty" toml:"host"`
	QEMU          QEMUInput          `json:"qemu,omitempty" toml:"qemu"`
	Machine       MachineInput       `json:"machine,omitempty" toml:"machine"`
	Kernel        KernelInput        `json:"kernel" toml:"kernel"`
	Graphics      *GraphicsInput     `json:"graphics,omitempty" toml:"graphics"`
	Mounts        MountsInput        `json:"mounts,omitempty" toml:"mounts"`
	Workspace     WorkspaceInput     `json:"workspace,omitempty" toml:"workspace"`
	Networks      []NetworkInput     `json:"networks,omitempty" toml:"networks"`
	Balloon       *BalloonInput      `json:"balloon,omitempty" toml:"balloon"`
	SSH           SSHInput           `json:"ssh,omitempty" toml:"ssh"`
	VSock         VSockInput         `json:"vsock,omitempty" toml:"vsock"`
	WriteFiles    []WriteFileInput   `json:"write_files,omitempty" toml:"write_files"`
	Notifications NotificationsInput `json:"notifications,omitempty" toml:"notifications"`
	Run           []RunInput         `json:"run,omitempty" toml:"run"`
	Hotplug       HotplugInput       `json:"hotplug,omitempty" toml:"hotplug"`
}

type HostInput struct {
	OS     string `json:"-" toml:"-"`
	Arch   string `json:"-" toml:"-"`
	System string `json:"-" toml:"-"`
}

type QEMUInput struct {
	Exec          []string `json:"exec,omitempty" toml:"exec"`
	FwdTunnelExec []string `json:"fwd_tunnel_exec,omitempty" toml:"fwd_tunnel_exec"`
	// Pointer preserves omitted vs explicitly empty input until resolution.
	User             *string           `json:"user,omitempty" toml:"user"`
	Seccomp          bool              `json:"seccomp,omitempty" toml:"seccomp"`
	MachineOptions   map[string]string `json:"machine_options,omitempty" toml:"machine_options"`
	QMPSocket        string            `json:"qmp_socket,omitempty" toml:"qmp_socket"`
	GuestAgentSocket string            `json:"guest_agent_socket,omitempty" toml:"guest_agent_socket"`
}

type MachineInput struct {
	Type string `json:"type,omitempty" toml:"type"`
	// Pointer preserves omitted vs explicitly zero input until resolution.
	VCPU *int `json:"vcpu,omitempty" toml:"vcpu"`
	// Pointer preserves omitted vs explicitly empty input until resolution.
	ID     *string   `json:"id,omitempty" toml:"id"`
	Memory units.MiB `json:"memory,omitempty" toml:"memory"`
	CPU    string    `json:"cpu,omitempty" toml:"cpu"`
	// Pointer preserves omitted vs explicitly false input until resolution.
	KVM *bool `json:"kvm,omitempty" toml:"kvm"`
}

type KernelInput struct {
	Path       string   `json:"path" toml:"path"`
	InitrdPath string   `json:"initrd_path" toml:"initrd_path"`
	Params     []string `json:"params,omitempty" toml:"params"`
	Serial     string   `json:"serial,omitempty" toml:"serial"`
}

type GraphicsInput struct {
	Backend string `json:"backend,omitempty" toml:"backend"`
}

type MountsInput []MountEntry

func (m MountsInput) RequiresPCI() bool {
	return len(m.VirtioFS()) > 0 || len(m.NineP()) > 0
}

type MountEntry interface {
	mountEntry()
	mountType() string
	resolveQEMUMount(*mountResolveContext) QEMUMountDevice
}

const (
	MountTypeVirtioFS = "virtiofs"
	MountTypeNineP    = "9p"
	MountTypeImage    = "image"
)

var mountRegistry = tagged.Registry[MountEntry]{
	tagged.Value[MountEntry, VirtioFSMountInput](MountTypeVirtioFS),
	tagged.Value[MountEntry, NinePMountInput](MountTypeNineP),
	tagged.Value[MountEntry, ImageMountInput](MountTypeImage),
}

func (m MountsInput) VirtioFS() []VirtioFSMountInput {
	return filterMounts[VirtioFSMountInput](m)
}

func (m MountsInput) NineP() []NinePMountInput {
	return filterMounts[NinePMountInput](m)
}

func (m MountsInput) Image() []ImageMountInput {
	return filterMounts[ImageMountInput](m)
}

func (m *MountsInput) UnmarshalJSON(data []byte) error {
	mounts, err := tagged.DecodeJSONList(data, "manifest.mounts", mountRegistry)
	*m = mounts
	return err
}

func (m MountsInput) MarshalJSON() ([]byte, error) {
	return tagged.MarshalJSONList(m, func(mount MountEntry) string {
		return mount.mountType()
	})
}

func (m *MountsInput) UnmarshalTOML(data any) error {
	mounts, err := tagged.DecodeTOMLList(data, "manifest.mounts", mountRegistry)
	*m = mounts
	return err
}

func filterMounts[T MountEntry](mounts MountsInput) []T {
	filtered := make([]T, 0, len(mounts))
	for _, mount := range mounts {
		if typed, ok := mount.(T); ok {
			filtered = append(filtered, typed)
		}
	}
	return filtered
}

type MountInput struct {
	Tag        string `json:"tag" toml:"tag"`
	SourcePath string `json:"source,omitempty" toml:"source"`
	ReadOnly   bool   `json:"read_only,omitempty" toml:"read_only"`
}

type VirtioFSMountInput struct {
	Type string `json:"type" toml:"type"`
	MountInput
	Target string `json:"target,omitempty" toml:"target"`

	VirtioFS VirtioFSInput `json:"virtiofs,omitempty" toml:"virtiofs"`
}

func (VirtioFSMountInput) mountEntry() {}

func (VirtioFSMountInput) mountType() string { return MountTypeVirtioFS }

type VirtioFSInput struct {
	Socket string   `json:"socket,omitempty" toml:"socket"`
	Bin    string   `json:"bin,omitempty" toml:"bin"`
	Args   []string `json:"args,omitempty" toml:"args"`
}

type NinePMountInput struct {
	Type string `json:"type" toml:"type"`
	MountInput

	NineP NinePInput `json:"9p,omitempty" toml:"9p"`
}

func (NinePMountInput) mountEntry() {}

func (NinePMountInput) mountType() string { return MountTypeNineP }

type NinePInput struct {
	SecurityModel string `json:"security_model,omitempty" toml:"security_model"`
}

type ImageMountInput struct {
	Type       string     `json:"type" toml:"type"`
	SourcePath string     `json:"source" toml:"source"`
	ReadOnly   bool       `json:"read_only,omitempty" toml:"read_only"`
	Image      ImageInput `json:"image,omitempty" toml:"image"`
}

func (ImageMountInput) mountEntry() {}

func (ImageMountInput) mountType() string { return MountTypeImage }

type ImageInput struct {
	Size       units.MiB `json:"size,omitempty" toml:"size"`
	FSType     string    `json:"fs,omitempty" toml:"fs"`
	Format     string    `json:"format,omitempty" toml:"format"`
	AutoCreate bool      `json:"create,omitempty" toml:"create"`
	// Pointer preserves omitted vs explicitly empty input until resolution.
	Label  *string `json:"label,omitempty" toml:"label"`
	Direct bool    `json:"direct,omitempty" toml:"direct"`
	// Pointer preserves omitted vs explicitly empty input until resolution.
	Serial *string `json:"serial,omitempty" toml:"serial"`
}

type WorkspaceInput struct {
	GuestDir string `json:"guest_dir,omitempty" toml:"guest_dir"`
	HostDir  string `json:"host_dir,omitempty" toml:"host_dir"`
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
	MinActual             units.MiB `json:"min_actual,omitempty" toml:"min_actual"`
	MaxActual             units.MiB `json:"max_actual,omitempty" toml:"max_actual"`
	GrowBelowAvailable    units.MiB `json:"grow_below_available,omitempty" toml:"grow_below_available"`
	ReclaimAboveAvailable units.MiB `json:"reclaim_above_available,omitempty" toml:"reclaim_above_available"`
	Step                  units.MiB `json:"step,omitempty" toml:"step"`
	PollIntervalSeconds   int       `json:"poll_interval_seconds,omitempty" toml:"poll_interval_seconds"`
	ReclaimHoldoffSeconds int       `json:"reclaim_holdoff_seconds,omitempty" toml:"reclaim_holdoff_seconds"`
}

type SSHInput struct {
	Exec        []string `json:"exec,omitempty" toml:"exec"`
	User        string   `json:"user,omitempty" toml:"user"`
	ReadySocket string   `json:"ready_socket,omitempty" toml:"ready_socket"`
	// Pointer preserves omitted vs explicitly zero input until resolution.
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
	GuestPath string `json:"guest_path" toml:"guest_path"`
	// Pointer preserves omitted vs explicitly empty input until resolution.
	Chown *string `json:"chown,omitempty" toml:"chown"`
	// Pointer preserves omitted vs explicitly empty input until resolution.
	Text *string `json:"text,omitempty" toml:"text"`
	// Pointer preserves omitted vs explicitly empty input until resolution.
	Mode *string `json:"mode,omitempty" toml:"mode"`
	// Pointer preserves omitted vs explicitly false input until resolution.
	Overwrite *bool `json:"overwrite,omitempty" toml:"overwrite"`
	// Pointer preserves omitted vs explicitly false input until resolution.
	FollowLinks *bool `json:"follow_links,omitempty" toml:"follow_links"`
	// Pointer preserves omitted vs explicitly false input until resolution.
	WriteBack *bool `json:"write_back,omitempty" toml:"write_back"`
	// Pointer preserves omitted vs explicitly empty input until resolution.
	Path *string `json:"source,omitempty" toml:"source"`
}

type NotificationsInput struct {
	Exec   []string `json:"exec,omitempty" toml:"exec"`
	States []string `json:"states,omitempty" toml:"states"`
}

type RunInput struct {
	Exec []string       `json:"exec" toml:"exec"`
	Vars map[string]any `json:"vars,omitempty" toml:"vars"`
}

type HotplugInput struct {
	Mounts   MountsInput    `json:"mounts,omitempty" toml:"mounts"`
	Networks []NetworkInput `json:"networks,omitempty" toml:"networks"`
}

func (h HotplugInput) Len() int {
	return len(h.Mounts) + len(h.Networks)
}

func (h HotplugInput) VirtioFS() []VirtioFSMountInput {
	return h.Mounts.VirtioFS()
}
