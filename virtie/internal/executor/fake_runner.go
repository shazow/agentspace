package executor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// FakeStart records a command started by FakeRunner.
type FakeStart struct {
	Name         string
	Args         []string
	Env          []string
	EnvAdditions []string
	Dir          string
	ProcessGroup bool
	Cmd          *exec.Cmd
}

// FakeRunnerSignal records a signal or kill observed by a fake runner process.
type FakeRunnerSignal struct {
	Name   string
	Signal os.Signal
}

// FakeRunner is a test implementation of the Runner Start API.
type FakeRunner struct {
	OnStart     func(FakeStart) (*FakeProcess, error)
	StartErrors map[string]error

	mu             sync.Mutex
	starts         []FakeStart
	processes      map[string][]*FakeProcess
	processSignals []FakeRunnerSignal
}

// Start records cmd and returns a fake process.
func (r *FakeRunner) Start(cmd *exec.Cmd) (*Process, error) {
	start := fakeStart(cmd)

	r.mu.Lock()
	r.starts = append(r.starts, start)
	err := r.StartErrors[start.Name]
	onStart := r.OnStart
	r.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if onStart != nil {
		process, err := onStart(start)
		if err != nil {
			return nil, err
		}
		if process == nil {
			process = r.NewProcess(start.Name)
		} else {
			r.configureProcess(start.Name, process)
			r.trackProcess(start.Name, process)
		}
		return process.Process(), nil
	}

	return r.NewProcess(start.Name).Process(), nil
}

// NewProcess returns a tracked fake process with signal recording callbacks.
func (r *FakeRunner) NewProcess(name string) *FakeProcess {
	process := &FakeProcess{FakeName: name}
	r.configureProcess(name, process)
	r.trackProcess(name, process)
	return process
}

// ExitedProcess returns a tracked fake process that has already exited.
func (r *FakeRunner) ExitedProcess(name string, err error) *FakeProcess {
	process := r.NewProcess(name)
	process.Exited = true
	process.WaitErr = err
	return process
}

// Starts returns recorded command starts.
func (r *FakeRunner) Starts() []FakeStart {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]FakeStart(nil), r.starts...)
}

// StartedNames returns recorded process names in start order.
func (r *FakeRunner) StartedNames() []string {
	starts := r.Starts()
	names := make([]string, 0, len(starts))
	for _, start := range starts {
		names = append(names, start.Name)
	}
	return names
}

// Processes returns processes created for name.
func (r *FakeRunner) Processes(name string) []*FakeProcess {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*FakeProcess(nil), r.processes[name]...)
}

// LastProcess returns the most recent process created for name.
func (r *FakeRunner) LastProcess(name string) *FakeProcess {
	processes := r.Processes(name)
	if len(processes) == 0 {
		return nil
	}
	return processes[len(processes)-1]
}

// ProcessSignals returns signal and kill events in observed order.
func (r *FakeRunner) ProcessSignals() []FakeRunnerSignal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]FakeRunnerSignal(nil), r.processSignals...)
}

// SignalNames returns process names for signal and kill events.
func (r *FakeRunner) SignalNames() []string {
	signals := r.ProcessSignals()
	names := make([]string, 0, len(signals))
	for _, signal := range signals {
		names = append(names, signal.Name)
	}
	return names
}

func (r *FakeRunner) trackProcess(name string, process *FakeProcess) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.processes == nil {
		r.processes = make(map[string][]*FakeProcess)
	}
	r.processes[name] = append(r.processes[name], process)
}

func (r *FakeRunner) configureProcess(name string, process *FakeProcess) {
	onSignal := process.OnSignal
	onKill := process.OnKill
	process.OnSignal = func(sig os.Signal) {
		if onSignal != nil {
			onSignal(sig)
		}
		r.recordProcessSignal(name, sig)
	}
	process.OnKill = func() {
		if onKill != nil {
			onKill()
		}
		r.recordProcessSignal(name, nil)
	}
}

func (r *FakeRunner) recordProcessSignal(name string, sig os.Signal) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.processSignals = append(r.processSignals, FakeRunnerSignal{Name: name, Signal: sig})
}

func fakeStart(cmd *exec.Cmd) FakeStart {
	name := "command"
	if cmd != nil {
		if len(cmd.Args) > 0 && cmd.Args[0] != "" {
			name = filepath.Base(cmd.Args[0])
		} else if cmd.Path != "" {
			name = filepath.Base(cmd.Path)
		}
	}
	return FakeStart{
		Name:         name,
		Args:         fakeCommandArgs(cmd),
		Env:          fakeCommandEnv(cmd),
		EnvAdditions: fakeCommandEnvAdditions(fakeCommandEnv(cmd)),
		Dir:          fakeCommandDir(cmd),
		ProcessGroup: fakeCommandProcessGroup(cmd),
		Cmd:          cmd,
	}
}

func fakeCommandArgs(cmd *exec.Cmd) []string {
	if cmd == nil || len(cmd.Args) == 0 {
		return nil
	}
	return append([]string(nil), cmd.Args[1:]...)
}

func fakeCommandEnv(cmd *exec.Cmd) []string {
	if cmd == nil {
		return nil
	}
	return append([]string(nil), cmd.Env...)
}

func fakeCommandEnvAdditions(env []string) []string {
	environ := os.Environ()
	if len(env) < len(environ) {
		return append([]string(nil), env...)
	}
	for i, entry := range environ {
		if env[i] != entry {
			return append([]string(nil), env...)
		}
	}
	return append([]string(nil), env[len(environ):]...)
}

func fakeCommandDir(cmd *exec.Cmd) string {
	if cmd == nil {
		return ""
	}
	return cmd.Dir
}

func fakeCommandProcessGroup(cmd *exec.Cmd) bool {
	return cmd != nil && cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid
}

func (s FakeStart) String() string {
	return fmt.Sprintf("%s %v", s.Name, s.Args)
}

var _ interface {
	Start(*exec.Cmd) (*Process, error)
} = (*FakeRunner)(nil)
