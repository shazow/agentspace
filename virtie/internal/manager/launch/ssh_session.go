package launch

import (
	"context"
	"io"
	"log/slog"
	"time"

	shellquote "github.com/kballard/go-shellquote"
	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/manifest"
	"github.com/shazow/agentspace/virtie/internal/sshtools"
)

type SSHSessionStats interface {
	MarkSSHAttempt(time.Time)
	MarkSSHStarted(time.Time)
}

type SSHSessionProcesses interface {
	Add(...*executor.Process)
	Remove(*executor.Process) bool
	Watchers() executor.Group
}

type SSHAutoprovisionKey struct {
	IdentityFile  string
	PublicKeyFile string
	AuthorizedKey string
}

type SSHSession struct {
	Plan                   *Plan
	Runner                 Runner
	Processes              SSHSessionProcesses
	Stats                  SSHSessionStats
	Logger                 *slog.Logger
	Output                 io.Writer
	RetryOutputRevealDelay time.Duration

	Wait         func(context.Context, *executor.Process, executor.Group) error
	WaitForRetry func(context.Context, executor.Group) error
	EnsureKey    func(*manifest.Manifest) (SSHAutoprovisionKey, error)
	InstallKey   func(context.Context, *manifest.Manifest, SSHAutoprovisionKey, executor.Group) error
	WrapStage    func(stage string, err error) error
	Now          func() time.Time
}

func RunSSHSession(ctx context.Context, session SSHSession) error {
	plan := session.Plan
	launchManifest := plan.Manifest
	argv := append([]string(nil), launchManifest.SSH.Argv...)
	sessionLogger := session.Logger
	if sessionLogger == nil {
		sessionLogger = slog.New(slog.DiscardHandler)
	}
	retryLog := sshtools.NewRetryLogger(sessionLogger)
	provisioned := false

	for {
		stderr := sshtools.NewRetryOutput(session.Output, false, session.RetryOutputRevealDelay)
		attemptStarted := sshSessionNow(session)
		if session.Stats != nil {
			session.Stats.MarkSSHAttempt(attemptStarted)
		}
		cmd, err := BuildSSHCommandWithArgv(launchManifest, plan.CID, plan.RemoteCommand, argv)
		if err != nil {
			return wrapSSHSessionStage(session, "active session", err)
		}
		sessionLogger.Info("ssh command", "command", shellquote.Join(cmd.Args...))
		cmd.Stderr = stderr
		started, err := session.Runner.Start(cmd)
		if err != nil {
			return wrapSSHSessionStage(session, "active session", err)
		}
		watchers := session.Processes.Watchers()
		session.Processes.Add(started)
		if session.Stats != nil {
			session.Stats.MarkSSHStarted(attemptStarted)
		}

		err = session.Wait(ctx, started, watchers)
		stderrText := stderr.String()
		if err == nil {
			stderr.Flush()
			return nil
		}
		if sshtools.ClassifyFailure(err, stderrText) == sshtools.FailureTransient {
			stderr.Suppress()
			retryLog.Log(err, stderrText)
			session.Processes.Remove(started)
			if session.WaitForRetry != nil {
				if waitErr := session.WaitForRetry(ctx, watchers); waitErr != nil {
					return waitErr
				}
			}
			continue
		}
		if launchManifest.SSH.Autoprovision && !provisioned && sshtools.ClassifyFailure(err, stderrText) == sshtools.FailureAuthentication {
			stderr.Suppress()
			session.Processes.Remove(started)
			sessionLogger.Info("ssh authentication failed; autoprovisioning a key", "state_dir", launchManifest.ResolvedPersistenceStateDir(), "user", launchManifest.SSH.User)
			key, keyErr := session.EnsureKey(launchManifest)
			if keyErr != nil {
				return wrapSSHSessionStage(session, "ssh autoprovision", keyErr)
			}
			if installErr := session.InstallKey(ctx, launchManifest, key, watchers); installErr != nil {
				return installErr
			}
			sessionLogger.Info("installed autoprovisioned ssh key; retrying ssh", "identity_file", key.IdentityFile, "public_key_file", key.PublicKeyFile)
			argv = (sshtools.Config{Exec: launchManifest.SSH.Argv, User: launchManifest.SSH.User}).WithIdentity(key.IdentityFile).Exec
			provisioned = true
			continue
		}
		stderr.Flush()
		return err
	}
}

func sshSessionNow(session SSHSession) time.Time {
	if session.Now != nil {
		return session.Now()
	}
	return time.Now()
}

func wrapSSHSessionStage(session SSHSession, stage string, err error) error {
	if session.WrapStage != nil {
		return session.WrapStage(stage, err)
	}
	return WrapStage(stage, err)
}
