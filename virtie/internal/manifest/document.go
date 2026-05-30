package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"

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
}

type HostInput struct {
	OS     string `json:"-" toml:"-"`
	Arch   string `json:"-" toml:"-"`
	System string `json:"-" toml:"-"`
}

type QEMUInput struct {
	Exec          []string `json:"exec,omitempty" toml:"exec"`
	FwdTunnelExec []string `json:"fwd_tunnel_exec,omitempty" toml:"fwd_tunnel_exec"`
	// Pointer preserves omitted vs explicitly empty input until lowering.
	User             *string           `json:"user,omitempty" toml:"user"`
	Seccomp          bool              `json:"seccomp,omitempty" toml:"seccomp"`
	MachineOptions   map[string]string `json:"machine_options,omitempty" toml:"machine_options"`
	QMPSocket        string            `json:"qmp_socket,omitempty" toml:"qmp_socket"`
	GuestAgentSocket string            `json:"guest_agent_socket,omitempty" toml:"guest_agent_socket"`
}

type MachineInput struct {
	Type string `json:"type,omitempty" toml:"type"`
	// Pointer preserves omitted vs explicitly zero input until lowering.
	VCPU *int `json:"vcpu,omitempty" toml:"vcpu"`
	// Pointer preserves omitted vs explicitly empty input until lowering.
	ID     *string   `json:"id,omitempty" toml:"id"`
	Memory units.MiB `json:"memory,omitempty" toml:"memory"`
	CPU    string    `json:"cpu,omitempty" toml:"cpu"`
	// Pointer preserves omitted vs explicitly false input until lowering.
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

func (m MountsInput) Len() int {
	return len(m)
}

type MountEntry interface {
	mountEntry()
	mountType() string
}

const (
	MountTypeVirtioFS = "virtiofs"
	MountTypeNineP    = "9p"
	MountTypeImage    = "image"
)

func (m MountsInput) VirtioFS() []VirtioFSMountInput {
	mounts := make([]VirtioFSMountInput, 0, len(m))
	for _, mount := range m {
		switch typed := mount.(type) {
		case VirtioFSMountInput:
			mounts = append(mounts, typed)
		case *VirtioFSMountInput:
			if typed != nil {
				mounts = append(mounts, *typed)
			}
		}
	}
	return mounts
}

func (m MountsInput) NineP() []NinePMountInput {
	mounts := make([]NinePMountInput, 0, len(m))
	for _, mount := range m {
		switch typed := mount.(type) {
		case NinePMountInput:
			mounts = append(mounts, typed)
		case *NinePMountInput:
			if typed != nil {
				mounts = append(mounts, *typed)
			}
		}
	}
	return mounts
}

func (m MountsInput) Image() []ImageMountInput {
	mounts := make([]ImageMountInput, 0, len(m))
	for _, mount := range m {
		switch typed := mount.(type) {
		case ImageMountInput:
			mounts = append(mounts, typed)
		case *ImageMountInput:
			if typed != nil {
				mounts = append(mounts, *typed)
			}
		}
	}
	return mounts
}

func (m *MountsInput) UnmarshalJSON(data []byte) error {
	var rawMounts []json.RawMessage
	if err := json.Unmarshal(data, &rawMounts); err != nil {
		return err
	}
	mounts := make(MountsInput, 0, len(rawMounts))
	for i, raw := range rawMounts {
		mount, err := decodeJSONMount(raw, i)
		if err != nil {
			return err
		}
		mounts = append(mounts, mount)
	}
	*m = mounts
	return nil
}

func (m MountsInput) MarshalJSON() ([]byte, error) {
	values := make([]any, 0, len(m))
	for _, mount := range m {
		value, err := mountMarshalValue(mount)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return json.Marshal(values)
}

func (m *MountsInput) UnmarshalTOML(data any) error {
	rawMounts, ok := data.([]map[string]any)
	if !ok {
		return fmt.Errorf("manifest.mounts must be an array of tables")
	}
	mounts := make(MountsInput, 0, len(rawMounts))
	for i, raw := range rawMounts {
		mount, err := decodeMapMount(raw, i)
		if err != nil {
			return err
		}
		mounts = append(mounts, mount)
	}
	*m = mounts
	return nil
}

func decodeJSONMount(data []byte, index int) (MountEntry, error) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, fmt.Errorf("manifest.mounts[%d]: %w", index, err)
	}
	return decodeMountData(data, header.Type, index)
}

func decodeMapMount(data map[string]any, index int) (MountEntry, error) {
	rawType, ok := data["type"].(string)
	if !ok {
		return nil, fmt.Errorf("manifest.mounts[%d].type must be one of virtiofs, 9p, or image", index)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("manifest.mounts[%d]: %w", index, err)
	}
	return decodeMountData(encoded, rawType, index)
}

func decodeMountData(data []byte, mountType string, index int) (MountEntry, error) {
	switch mountType {
	case MountTypeVirtioFS:
		var mount VirtioFSMountInput
		if err := decodeMountJSON(data, &mount); err != nil {
			return nil, fmt.Errorf("manifest.mounts[%d]: %w", index, err)
		}
		return mount, nil
	case MountTypeNineP:
		var mount NinePMountInput
		if err := decodeMountJSON(data, &mount); err != nil {
			return nil, fmt.Errorf("manifest.mounts[%d]: %w", index, err)
		}
		return mount, nil
	case MountTypeImage:
		var mount ImageMountInput
		if err := decodeMountJSON(data, &mount); err != nil {
			return nil, fmt.Errorf("manifest.mounts[%d]: %w", index, err)
		}
		return mount, nil
	default:
		return nil, fmt.Errorf("manifest.mounts[%d].type must be one of virtiofs, 9p, or image", index)
	}
}

func decodeMountJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func mountMarshalValue(mount MountEntry) (any, error) {
	switch typed := mount.(type) {
	case VirtioFSMountInput:
		typed.Type = MountTypeVirtioFS
		return typed, nil
	case *VirtioFSMountInput:
		if typed == nil {
			return nil, fmt.Errorf("unsupported nil mount input")
		}
		value := *typed
		value.Type = MountTypeVirtioFS
		return value, nil
	case NinePMountInput:
		typed.Type = MountTypeNineP
		return typed, nil
	case *NinePMountInput:
		if typed == nil {
			return nil, fmt.Errorf("unsupported nil mount input")
		}
		value := *typed
		value.Type = MountTypeNineP
		return value, nil
	case ImageMountInput:
		typed.Type = MountTypeImage
		return typed, nil
	case *ImageMountInput:
		if typed == nil {
			return nil, fmt.Errorf("unsupported nil mount input")
		}
		value := *typed
		value.Type = MountTypeImage
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported mount input type %T", mount)
	}
}

type MountInput struct {
	Tag        string `json:"tag" toml:"tag"`
	SourcePath string `json:"source,omitempty" toml:"source"`
	ReadOnly   bool   `json:"read_only,omitempty" toml:"read_only"`
}

type VirtioFSMountInput struct {
	Type string `json:"type" toml:"type"`
	MountInput

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
	AutoCreate bool      `json:"create,omitempty" toml:"create"`
	// Pointer preserves omitted vs explicitly empty input until lowering.
	Label  *string `json:"label,omitempty" toml:"label"`
	Direct bool    `json:"direct,omitempty" toml:"direct"`
	// Pointer preserves omitted vs explicitly empty input until lowering.
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
	// Pointer preserves omitted vs explicitly zero input until lowering.
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
	// Pointer preserves omitted vs explicitly empty input until lowering.
	Chown *string `json:"chown,omitempty" toml:"chown"`
	// Pointer preserves omitted vs explicitly empty input until lowering.
	Text *string `json:"text,omitempty" toml:"text"`
	// Pointer preserves omitted vs explicitly empty input until lowering.
	Mode *string `json:"mode,omitempty" toml:"mode"`
	// Pointer preserves omitted vs explicitly false input until lowering.
	Overwrite *bool `json:"overwrite,omitempty" toml:"overwrite"`
	// Pointer preserves omitted vs explicitly false input until lowering.
	FollowLinks *bool `json:"follow_links,omitempty" toml:"follow_links"`
	// Pointer preserves omitted vs explicitly false input until lowering.
	WriteBack *bool `json:"write_back,omitempty" toml:"write_back"`
	// Pointer preserves omitted vs explicitly empty input until lowering.
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
