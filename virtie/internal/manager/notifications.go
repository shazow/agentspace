package manager

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/shazow/agentspace/virtie/internal/executor"
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

type commandNotifier struct {
	command manifest.Command
	states  map[string]struct{}
	dir     string
	runner  runner
	logger  *slog.Logger
}

func (m *manager) effectiveNotifier(manifest *manifest.Manifest) notificationSink {
	if m.notifier != nil {
		return m.notifier
	}
	return newCommandNotifier(manifest, m.logger, m.runner)
}

func newCommandNotifier(manifest *manifest.Manifest, logger *slog.Logger, runner runner) notificationSink {
	if manifest == nil {
		return noopNotifier{}
	}
	notifications := manifest.Notifications
	if notifications.Command.IsZero() || notifications.Command.Path == "" {
		return noopNotifier{}
	}
	if runner == nil {
		runner = &executor.Runner{}
	}
	dir := resolvedNotificationWorkingDir(manifest.Paths.WorkingDir)
	command := notifications.Command
	command.Args = append([]string(nil), command.Args...)
	command.Env = append([]string(nil), command.Env...)

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
	renderer, err := manifest.NewTemplateRenderer(manifest.NotificationTemplateProvider{
		State:   state,
		Message: message,
		Values:  values,
	})
	if err != nil {
		if n.logger != nil {
			n.logger.Info("notification hook template failed", "state", state, "err", err)
		}
		return
	}
	command, err := manifest.RenderCommand(n.command, renderer)
	if err != nil {
		if n.logger != nil {
			n.logger.Info("notification hook template failed", "state", state, "err", err)
		}
		return
	}
	env, err := notificationEnv(state, message, values)
	if err != nil {
		if n.logger != nil {
			n.logger.Info("notification hook template failed", "state", state, "err", err)
		}
		return
	}
	env = append(env, command.Env...)
	cmd := executor.Command(command.Path, command.Args, env)
	cmd.Dir = n.dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := runNotificationCommand(ctx, n.runner, cmd); err != nil && n.logger != nil {
		n.logger.Info("notification hook failed", "state", state, "err", err)
	}
}

func runNotificationCommand(ctx context.Context, runner runner, cmd *exec.Cmd) error {
	process, err := runner.Start(cmd)
	if err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- process.Wait()
		close(done)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = process.Kill()
		<-done
		return ctx.Err()
	}
}

func (n *commandNotifier) enabled(state string) bool {
	if len(n.states) == 0 {
		return true
	}
	_, ok := n.states[state]
	return ok
}

func notificationEnv(state string, message string, values map[string]string) ([]string, error) {
	env := []string{
		"VIRTIE_NOTIFY_STATE=" + state,
		"VIRTIE_NOTIFY_MESSAGE=" + message,
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		envKey, err := executor.EnvName(key)
		if err != nil {
			return nil, err
		}
		env = append(env, fmt.Sprintf("VIRTIE_NOTIFY_CONTEXT_%s=%s", envKey, values[key]))
	}
	return env, nil
}
