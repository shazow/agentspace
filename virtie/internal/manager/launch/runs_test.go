package launch

import (
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/executor/executortest"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestStartRunsStartsResolvedRunCommands(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := runManifest(tmpDir)
	runner := &recordingRunRunner{}

	group, err := StartRuns(RunStarter{Runner: runner, ShutdownDelay: time.Millisecond}, 7, cfg)
	if err != nil {
		t.Fatalf("start runs: %v", err)
	}
	defer group.StopAll(time.Millisecond)

	if got, want := runner.names, []string{"proxy"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("started names: got %#v want %#v", got, want)
	}
	if got, want := runner.dirs["proxy"], tmpDir; got != want {
		t.Fatalf("run dir: got %q want %q", got, want)
	}
	if got, want := runner.args["proxy"], []string{"--cid=7", "--name=cache"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("run args: got %#v want %#v", got, want)
	}
}

func TestStartRunsStopsStartedProcessesOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := runManifest(tmpDir)
	cfg.Run = append(cfg.Run, manifest.Run{Exec: []string{"/bin/second"}})
	runner := &recordingRunRunner{failAt: 1, err: errors.New("start second failed")}

	if _, err := StartRuns(RunStarter{Runner: runner, ShutdownDelay: time.Millisecond}, 7, cfg); err == nil || !errors.Is(err, runner.err) {
		t.Fatalf("expected start failure, got %v", err)
	}
	if len(runner.processes) == 0 {
		t.Fatal("expected first process to start")
	}
	if exited, err := runner.processes[0].PollExit(); err != nil || !exited {
		t.Fatalf("expected started process to be stopped, exited=%v err=%v", exited, err)
	}
}

func runManifest(tmpDir string) *manifest.Manifest {
	return &manifest.Manifest{
		Paths: manifest.Paths{WorkingDir: tmpDir},
		Run: []manifest.Run{{
			Exec: []string{"/bin/proxy", "--cid={{.CID}}", "--name={{.Name}}"},
			Vars: map[string]any{"Name": "cache"},
		}},
	}
}

type recordingRunRunner struct {
	names     []string
	dirs      map[string]string
	args      map[string][]string
	processes []*executor.Process
	failAt    int
	err       error
}

func (r *recordingRunRunner) Start(cmd *exec.Cmd) (*executor.Process, error) {
	if r.failAt > 0 && len(r.names) == r.failAt {
		return nil, r.err
	}
	if r.dirs == nil {
		r.dirs = make(map[string]string)
	}
	if r.args == nil {
		r.args = make(map[string][]string)
	}
	name := filepath.Base(cmd.Path)
	r.names = append(r.names, name)
	r.dirs[name] = cmd.Dir
	r.args[name] = append([]string(nil), cmd.Args[1:]...)
	process := (&executortest.Process{OverrideName: name}).Process()
	r.processes = append(r.processes, process)
	return process, nil
}
