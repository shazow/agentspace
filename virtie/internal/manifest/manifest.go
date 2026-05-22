// Package manifest defines the internal virtie launch contract.
//
// It owns the JSON schema that Nix emits for virtie, along with the defaulting
// and validation rules that keep the runtime assumptions consistent. The
// package also resolves working-directory and runtime-directory paths into the
// concrete host-side paths that the manager uses for sockets, lock files,
// volumes, QEMU binaries, and virtiofs daemons.
package manifest

type Manifest struct {
	Identity      Identity        `json:"identity"`
	Paths         Paths           `json:"paths"`
	Persistence   Persistence     `json:"persistence"`
	SSH           SSH             `json:"ssh"`
	QEMU          QEMU            `json:"qemu"`
	Volumes       []Volume        `json:"volumes,omitempty"`
	VSock         VSock           `json:"vsock"`
	VirtioFS      VirtioFS        `json:"virtiofs"`
	Workspace     Workspace       `json:"workspace,omitempty"`
	WriteFiles    WriteFiles      `json:"writeFiles,omitempty"`
	Notifications Notifications   `json:"notifications,omitempty"`
	RunWithTunnel []RunWithTunnel `json:"runWithTunnel,omitempty"`
}

type Identity struct {
	HostName string `json:"hostName"`
}

type Paths struct {
	WorkingDir string  `json:"workingDir"`
	LockPath   string  `json:"lockPath"`
	RuntimeDir *string `json:"runtimeDir,omitempty"`
}

type Persistence struct {
	Directories []string `json:"directories"`
	BaseDir     string   `json:"baseDir,omitempty"`
	StateDir    string   `json:"stateDir,omitempty"`
}

type SSH struct {
	Argv          []string `json:"argv"`
	User          string   `json:"user"`
	RetryDelay    *float64 `json:"retryDelay,omitempty"`
	Autoprovision bool     `json:"autoprovision,omitempty"`
}

type VSockCIDRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type VSock struct {
	CIDRange VSockCIDRange `json:"cidRange"`
}

type Workspace struct {
	BaseDir  string `json:"baseDir,omitempty"`
	MountCWD bool   `json:"mountCWD,omitempty"`
}

type Volume struct {
	ImagePath     string   `json:"imagePath"`
	SizeMiB       int      `json:"sizeMiB,omitempty"`
	FSType        string   `json:"fsType,omitempty"`
	AutoCreate    bool     `json:"autoCreate,omitempty"`
	Label         *string  `json:"label,omitempty"`
	MkfsExtraArgs []string `json:"mkfsExtraArgs,omitempty"`
}

type Command struct {
	Path string   `json:"path"`
	Args []string `json:"args,omitempty"`
	Env  []string `json:"env,omitempty"`
}

type Notifications struct {
	Command *Command `json:"command,omitempty"`
	States  []string `json:"states,omitempty"`
}

type VirtioFSDaemon struct {
	Tag        string  `json:"tag"`
	SocketPath string  `json:"socketPath"`
	Command    Command `json:"command"`
}

type VirtioFS struct {
	Daemons []VirtioFSDaemon `json:"daemons"`
}

type RunWithTunnel struct {
	SocketPath string            `json:"socketPath"`
	Exec       []string          `json:"exec"`
	Env        []string          `json:"env,omitempty"`
	Vars       map[string]string `json:"vars,omitempty"`
}

type WriteFile struct {
	Chown       *string `json:"chown,omitempty"`
	Text        *string `json:"text,omitempty"`
	Mode        *string `json:"mode,omitempty"`
	Overwrite   *bool   `json:"overwrite,omitempty"`
	FollowLinks *bool   `json:"followLinks,omitempty"`
	WriteBack   *bool   `json:"writeBack,omitempty"`
	Path        *string `json:"path,omitempty"`
}

type WriteFiles map[string]WriteFile

type ResolvedWriteFile struct {
	GuestPath   string
	Chown       *string
	Text        *string
	Mode        *string
	Overwrite   bool
	FollowLinks bool
	WriteBack   bool
	HostPath    *string
}

type ResolvedRunWithTunnel struct {
	SocketPath      string
	GuestSocketPath string
	Exec            []string
	Env             []string
	Dir             string
	Vars            map[string]string
}
