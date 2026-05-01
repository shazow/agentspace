package manager

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

const (
	notifyStateRuntimeSuspend = "runtime:suspend"
	notifyStateRuntimeResume  = "runtime:resume"
)

type notificationSink interface {
	Notify(ctx context.Context, state string, message string, values map[string]string)
}

type noopNotifier struct{}

func (noopNotifier) Notify(context.Context, string, string, map[string]string) {}

type notificationRunner interface {
	Run(ctx context.Context, path string, args []string, dir string, env []string) error
}

type execNotificationRunner struct{}

func (execNotificationRunner) Run(ctx context.Context, path string, args []string, dir string, env []string) error {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

type commandNotifier struct {
	command manifest.Command
	states  map[string]struct{}
	dir     string
	runner  notificationRunner
	logger  *slog.Logger
}

func (m *manager) effectiveNotifier(manifest *manifest.Manifest) notificationSink {
	if m.notifier != nil {
		return m.notifier
	}
	return newCommandNotifier(manifest, m.logger, m.notificationRunner)
}

func newCommandNotifier(manifest *manifest.Manifest, logger *slog.Logger, runner notificationRunner) notificationSink {
	if manifest == nil {
		return noopNotifier{}
	}
	notifications := manifest.Notifications
	if notifications.Command == nil || notifications.Command.Path == "" {
		return noopNotifier{}
	}
	if runner == nil {
		runner = execNotificationRunner{}
	}
	dir := resolvedNotificationWorkingDir(manifest.Paths.WorkingDir)
	command := *notifications.Command
	if !filepath.IsAbs(command.Path) {
		command.Path = filepath.Join(dir, command.Path)
	}
	command.Args = append([]string(nil), notifications.Command.Args...)

	var states map[string]struct{}
	if len(notifications.States) > 0 {
		states = make(map[string]struct{}, len(notifications.States))
		for _, state := range notifications.States {
			states[state] = struct{}{}
		}
	}

	return &commandNotifier{
		command: command,
		states:  states,
		dir:     dir,
		runner:  runner,
		logger:  logger,
	}
}

func resolvedNotificationWorkingDir(dir string) string {
	if filepath.IsAbs(dir) {
		return dir
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return absDir
}

func (n *commandNotifier) Notify(ctx context.Context, state string, message string, values map[string]string) {
	if n == nil || !n.enabled(state) {
		return
	}
	env := notificationEnv(state, message, values)
	if err := n.runner.Run(ctx, n.command.Path, n.command.Args, n.dir, env); err != nil && n.logger != nil {
		n.logger.Info("notification hook failed", "state", state, "err", err)
	}
}

func (n *commandNotifier) enabled(state string) bool {
	if len(n.states) == 0 {
		return true
	}
	_, ok := n.states[state]
	return ok
}

func notificationEnv(state string, message string, values map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	env = append(env,
		"VIRTIE_NOTIFY_STATE="+state,
		"VIRTIE_NOTIFY_MESSAGE="+message,
	)

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, fmt.Sprintf("VIRTIE_NOTIFY_CONTEXT_%s=%s", envKey(key), values[key]))
	}
	return env
}

func envKey(key string) string {
	var builder strings.Builder
	var previousUnderscore bool
	var previousLowerOrDigit bool
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if unicode.IsUpper(r) && previousLowerOrDigit && !previousUnderscore {
				builder.WriteByte('_')
			}
			builder.WriteRune(unicode.ToUpper(r))
			previousUnderscore = false
			previousLowerOrDigit = unicode.IsLower(r) || unicode.IsDigit(r)
			continue
		}
		if builder.Len() > 0 && !previousUnderscore {
			builder.WriteByte('_')
			previousUnderscore = true
		}
		previousLowerOrDigit = false
	}
	return strings.Trim(builder.String(), "_")
}
