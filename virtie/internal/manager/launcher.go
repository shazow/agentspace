package manager

import (
	"context"
	"os"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manager/launch"
	runtimepkg "github.com/shazow/agentspace/virtie/internal/manager/runtime"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/qga"
	"github.com/shazow/agentspace/virtie/internal/qmpclient"
)

type ResumeMode = launch.ResumeMode

const (
	ResumeModeNo    = launch.ResumeModeNo
	ResumeModeAuto  = launch.ResumeModeAuto
	ResumeModeForce = launch.ResumeModeForce
)

type LaunchOptions = launch.Options

type WaitMode = launch.WaitMode

const (
	WaitAuto = launch.WaitAuto
	WaitSSH  = launch.WaitSSH
	WaitVM   = launch.WaitVM
)

type Launcher struct {
	manager *manager
}

func DefaultConfig() Config {
	return Config{
		Locker:              &fileLocker{},
		VSockCIDChecker:     newHostVSockCIDChecker(),
		Runner:              &executor.Runner{},
		SocketWaiter:        &pollingSocketWaiter{},
		QMPDialer:           &qmpclient.SocketMonitorDialer{},
		GuestAgentDialer:    &qga.SocketDialer{},
		SSHReadyDialer:      &unixSSHReadyDialer{},
		Logger:              logger,
		LogWriter:           os.Stderr,
		SSHRetryDelay:       defaultSSHRetryDelay,
		SSHReadyTimeout:     configuredSSHReadyTimeout(),
		ShutdownDelay:       defaultShutdownDelay,
		QMPRetryDelay:       defaultQMPRetryDelay,
		QMPConnectTimeout:   defaultQMPConnectTimeout,
		QMPQuitTimeout:      defaultQMPQuitTimeout,
		QMPMigrationTimeout: defaultQMPMigrationTimeout,
	}
}

func NewLauncher(configs ...Config) *Launcher {
	config := DefaultConfig()
	if len(configs) > 0 {
		config = mergeConfig(config, configs[0])
	}
	return &Launcher{manager: newManagerFromConfig(config)}
}

func (l *Launcher) Plan(ctx context.Context, spec launch.Spec) (*launch.Plan, error) {
	_ = ctx
	if l == nil || l.manager == nil {
		l = NewLauncher()
	}
	return l.manager.planLaunch(spec)
}

func (l *Launcher) Start(ctx context.Context, plan *launch.Plan) (*runtimepkg.Runtime, error) {
	if l == nil || l.manager == nil {
		l = NewLauncher()
	}
	return l.manager.startWithPlan(ctx, plan)
}

// Launch runs the supported virtie sandbox session.
func Launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) error {
	return NewLauncher().launch(ctx, manifest, remoteCommand)
}

// LaunchWithOptions runs the supported virtie sandbox session with explicit launch options.
func LaunchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options LaunchOptions) error {
	return NewLauncher().launchWithOptions(ctx, manifest, remoteCommand, options)
}

func (l *Launcher) launch(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string) (err error) {
	return l.launchWithOptions(ctx, manifest, remoteCommand, launch.Options{Resume: launch.ResumeModeNo, SSH: true})
}

func (l *Launcher) launchWithOptions(ctx context.Context, manifest *manifest.Manifest, remoteCommand []string, options launch.Options) error {
	plan, err := l.Plan(ctx, launch.Spec{Manifest: manifest, RemoteCommand: remoteCommand, Options: options})
	if err != nil {
		return err
	}
	return l.manager.launchWithPlan(ctx, plan)
}
