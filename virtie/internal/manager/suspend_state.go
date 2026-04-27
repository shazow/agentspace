package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

type suspendState struct {
	HostName      string    `json:"hostName"`
	QMPSocketPath string    `json:"qmpSocketPath"`
	VMStatePath   string    `json:"vmStatePath,omitempty"`
	CID           int       `json:"cid,omitempty"`
	Timestamp     time.Time `json:"timestamp"`
	Status        string    `json:"status"`
}

func suspendStatePath(manifest *manifest.Manifest) string {
	return filepath.Join(manifest.ResolvedPersistenceStateDir(), manifest.Identity.HostName+".suspend.json")
}

func vmStatePath(manifest *manifest.Manifest) string {
	return filepath.Join(manifest.ResolvedPersistenceStateDir(), manifest.Identity.HostName+".vmstate")
}

func launchPIDPath(manifest *manifest.Manifest) string {
	return filepath.Join(manifest.ResolvedPersistenceStateDir(), manifest.Identity.HostName+".pid")
}

func writeSuspendState(manifest *manifest.Manifest, qmpSocketPath string, status string) error {
	return writeSuspendStateData(manifest, suspendState{
		HostName:      manifest.Identity.HostName,
		QMPSocketPath: qmpSocketPath,
		Timestamp:     time.Now().UTC(),
		Status:        status,
	})
}

func writeSuspendStateData(manifest *manifest.Manifest, state suspendState) error {
	if state.HostName == "" {
		state.HostName = manifest.Identity.HostName
	}
	if state.Timestamp.IsZero() {
		state.Timestamp = time.Now().UTC()
	}
	path := suspendStatePath(manifest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create suspend state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode suspend state: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write suspend state %q: %w", path, err)
	}
	return nil
}

func readSuspendState(manifest *manifest.Manifest) (suspendState, error) {
	path := suspendStatePath(manifest)
	data, err := os.ReadFile(path)
	if err != nil {
		return suspendState{}, err
	}

	var state suspendState
	if err := json.Unmarshal(data, &state); err != nil {
		return suspendState{}, fmt.Errorf("decode suspend state %q: %w", path, err)
	}
	return state, nil
}

func hasSavedSuspendState(manifest *manifest.Manifest) (bool, error) {
	state, err := readSuspendState(manifest)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return state.Status == "saved", nil
}

func removeSuspendState(manifest *manifest.Manifest) error {
	path := suspendStatePath(manifest)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove suspend state %q: %w", path, err)
	}
	return nil
}

type pidSignaler interface {
	Exists(pid int) error
	Signal(pid int, sig os.Signal) error
}

type syscallPIDSignaler struct{}

func writeLaunchPID(manifest *manifest.Manifest, pid int) error {
	path := launchPIDPath(manifest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create launch pid directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write launch pid %q: %w", path, err)
	}
	return nil
}

func readLaunchPID(manifest *manifest.Manifest) (int, error) {
	path := launchPIDPath(manifest)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("launch pid file %q does not exist; is virtie launch running for host %q?", path, manifest.Identity.HostName)
		}
		return 0, fmt.Errorf("read launch pid %q: %w", path, err)
	}

	raw := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 {
		if err == nil {
			err = fmt.Errorf("pid must be positive")
		}
		return 0, fmt.Errorf("invalid launch pid file %q: %w", path, err)
	}
	return pid, nil
}

func removeLaunchPID(manifest *manifest.Manifest, pid int) error {
	path := launchPIDPath(manifest)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read launch pid %q: %w", path, err)
	}
	current, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("invalid launch pid file %q: %w", path, err)
	}
	if current != pid {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove launch pid %q: %w", path, err)
	}
	return nil
}

func (syscallPIDSignaler) Exists(pid int) error {
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.EPERM) {
		return nil
	}
	return err
}

func (syscallPIDSignaler) Signal(pid int, sig os.Signal) error {
	number, ok := sig.(syscall.Signal)
	if !ok {
		return fmt.Errorf("unsupported signal %v", sig)
	}
	return syscall.Kill(pid, number)
}

func errorsIsNoProcess(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}

func validateLaunchLock(manifest *manifest.Manifest, pid int) error {
	path := manifest.ResolvedLockPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read launch lock %q: %w", path, err)
	}
	lockPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || lockPID <= 0 {
		if err == nil {
			err = fmt.Errorf("pid must be positive")
		}
		return fmt.Errorf("invalid launch lock %q: %w", path, err)
	}
	if lockPID != pid {
		return fmt.Errorf("launch pid %d does not match lock owner %d in %q", pid, lockPID, path)
	}

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open launch lock %q: %w", path, err)
	}
	defer file.Close()

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		return fmt.Errorf("stale launch pid %d from %q: sandbox lock %q is not held", pid, launchPIDPath(manifest), path)
	} else if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
		return fmt.Errorf("check launch lock %q: %w", path, err)
	}

	return nil
}
