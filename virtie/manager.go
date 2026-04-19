package virtie

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	DefaultSSHRetryDelay = 1 * time.Second
	DefaultShutdownDelay = 15 * time.Second
)

var SSHProbeCommand = []string{"true"}

type Manager struct {
	Locker        Locker
	Runner        Runner
	SocketWaiter  SocketWaiter
	Logger        *log.Logger
	SSHRetryDelay time.Duration
	ShutdownDelay time.Duration
}

func NewManager() *Manager {
	return &Manager{
		Locker:        &FileLocker{},
		Runner:        &ExecRunner{},
		SocketWaiter:  &PollingSocketWaiter{},
		Logger:        log.New(os.Stderr, "virtie: ", 0),
		SSHRetryDelay: DefaultSSHRetryDelay,
		ShutdownDelay: DefaultShutdownDelay,
	}
}

func (m *Manager) Launch(ctx context.Context, manifest *Manifest, remoteCommand []string) (err error) {
	if err := manifest.Validate(); err != nil {
		return err
	}

	lock, err := m.Locker.Acquire(manifest.ResolvedLockPath())
	if err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}
	defer func() {
		releaseErr := lock.Release()
		if err == nil {
			err = releaseErr
		} else if releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()

	if err := ensureDirectories(manifest.ResolvedPersistenceDirectories()); err != nil {
		return &StageError{Stage: "preflight", Err: err}
	}

	var started []*managedProcess
	defer func() {
		stopErr := m.stopAll(started)
		if err == nil {
			err = stopErr
		} else if stopErr != nil {
			err = errors.Join(err, stopErr)
		}
	}()

	m.Logger.Printf("starting virtiofsd-run")
	virtiofsd, err := m.startManagedProcess(ProcessSpec{
		Name:   "virtiofsd",
		Path:   manifest.ResolvedVirtioFSDRun(),
		Dir:    manifest.Paths.WorkingDir,
		Stdout: os.Stderr,
		Stderr: os.Stderr,
	})
	if err != nil {
		return &StageError{Stage: "virtiofs startup", Err: err}
	}
	started = append(started, virtiofsd)

	m.Logger.Printf("waiting for virtiofs sockets")
	if err := m.waitForSockets(ctx, manifest.ResolvedSocketPaths(), started...); err != nil {
		return err
	}

	m.Logger.Printf("starting microvm-run")
	microvm, err := m.startManagedProcess(ProcessSpec{
		Name:   "microvm",
		Path:   manifest.ResolvedMicroVMRun(),
		Dir:    manifest.Paths.WorkingDir,
		Stdout: os.Stderr,
		Stderr: os.Stderr,
	})
	if err != nil {
		return &StageError{Stage: "vm startup", Err: err}
	}
	started = append(started, microvm)

	m.Logger.Printf("waiting for ssh readiness")
	if err := m.waitForSSH(ctx, manifest, remoteCommand, started...); err != nil {
		return err
	}

	m.Logger.Printf("starting ssh session")
	session, err := m.startManagedProcess(buildSSHSpec(manifest, remoteCommand, true))
	if err != nil {
		return &StageError{Stage: "active session", Err: err}
	}
	started = append(started, session)

	return m.waitForSession(ctx, session, started[:len(started)-1]...)
}

type managedProcess struct {
	name string
	proc Process
	done chan error
}

func (m *Manager) startManagedProcess(spec ProcessSpec) (*managedProcess, error) {
	proc, err := m.Runner.Start(spec)
	if err != nil {
		return nil, err
	}

	mp := &managedProcess{
		name: spec.Name,
		proc: proc,
		done: make(chan error, 1),
	}

	go func() {
		mp.done <- proc.Wait()
		close(mp.done)
	}()

	return mp, nil
}

func (m *Manager) waitForSockets(ctx context.Context, socketPaths []string, watchers ...*managedProcess) error {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.SocketWaiter.Wait(waitCtx, socketPaths)
	}()

	ticker := time.NewTicker(DefaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				return &StageError{Stage: "virtiofs startup", Err: err}
			}
			return nil
		case <-ticker.C:
			if err := firstUnexpectedExit("virtiofs startup", watchers...); err != nil {
				return err
			}
		case <-ctx.Done():
			return &StageError{Stage: "virtiofs startup", Err: ctx.Err()}
		}
	}
}

func (m *Manager) waitForSSH(ctx context.Context, manifest *Manifest, remoteCommand []string, watchers ...*managedProcess) error {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return &StageError{Stage: "ssh readiness", Err: ctx.Err()}
		case <-timer.C:
		}

		if err := firstUnexpectedExit("ssh readiness", watchers...); err != nil {
			return err
		}

		probe, err := m.startManagedProcess(buildSSHSpec(manifest, SSHProbeCommand, false))
		if err != nil {
			return &StageError{Stage: "ssh readiness", Err: err}
		}

		if err := <-probe.done; err == nil {
			return nil
		}

		if err := firstUnexpectedExit("ssh readiness", watchers...); err != nil {
			return err
		}

		timer.Reset(m.SSHRetryDelay)
	}
}

func (m *Manager) waitForSession(ctx context.Context, session *managedProcess, watchers ...*managedProcess) error {
	ticker := time.NewTicker(DefaultSocketPollInterval)
	defer ticker.Stop()

	for {
		select {
		case err := <-session.done:
			if err != nil {
				return wrapCommandError("active session", session.name, err)
			}
			return nil
		case <-ticker.C:
			if err := firstUnexpectedExit("active session", watchers...); err != nil {
				return err
			}
		case <-ctx.Done():
			return &StageError{Stage: "active session", Err: ctx.Err()}
		}
	}
}

func (m *Manager) stopAll(processes []*managedProcess) error {
	var errs []error
	for i := len(processes) - 1; i >= 0; i-- {
		if err := m.stopProcess(processes[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) stopProcess(process *managedProcess) error {
	if process == nil {
		return nil
	}

	if exited, _ := process.pollExit(); exited {
		return nil
	}

	if err := process.proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("stop %s: %w", process.name, err)
	}

	timer := time.NewTimer(m.ShutdownDelay)
	defer timer.Stop()

	select {
	case <-process.done:
		return nil
	case <-timer.C:
		if err := process.proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("kill %s: %w", process.name, err)
		}
		<-process.done
		return nil
	}
}

func (p *managedProcess) pollExit() (bool, error) {
	select {
	case err, ok := <-p.done:
		if !ok {
			return true, nil
		}
		return true, err
	default:
		return false, nil
	}
}

func firstUnexpectedExit(stage string, processes ...*managedProcess) error {
	for _, process := range processes {
		if process == nil {
			continue
		}

		exited, err := process.pollExit()
		if !exited {
			continue
		}

		if err == nil {
			return &StageError{
				Stage: stage,
				Err:   fmt.Errorf("%s exited unexpectedly", process.name),
			}
		}

		return wrapCommandError(stage, process.name, err)
	}

	return nil
}

func buildSSHSpec(manifest *Manifest, remoteCommand []string, interactive bool) ProcessSpec {
	argv := append([]string(nil), manifest.SSH.Argv...)
	path := argv[0]
	args := append([]string(nil), argv[1:]...)
	stdin := io.Reader(nil)
	stdout := io.Writer(io.Discard)
	stderr := io.Writer(io.Discard)

	if !interactive {
		args = append(args, "-o", "BatchMode=yes")
	} else {
		stdin = os.Stdin
		stdout = os.Stdout
		stderr = os.Stderr
	}
	args = append(args, remoteCommand...)

	return ProcessSpec{
		Name:   "ssh",
		Path:   path,
		Args:   args,
		Dir:    manifest.Paths.WorkingDir,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}
}

func ensureDirectories(directories []string) error {
	for _, dir := range directories {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %q: %w", dir, err)
		}
	}
	return nil
}

func wrapCommandError(stage string, command string, err error) error {
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &CommandError{
			Stage:    stage,
			Command:  command,
			ExitCode: exitErr.ExitCode(),
			Err:      err,
		}
	}

	return &CommandError{
		Stage:    stage,
		Command:  command,
		ExitCode: -1,
		Err:      err,
	}
}
