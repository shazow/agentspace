package launch

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/executor/executortest"
)

func TestStartQEMUUsesPlanCommand(t *testing.T) {
	cmd := exec.Command("/bin/qemu")
	runner := &qemuRunner{}
	process, err := StartQEMU(runner, nil, &Plan{QEMUCommand: cmd})
	if err != nil {
		t.Fatalf("start qemu: %v", err)
	}
	if process == nil {
		t.Fatal("expected process")
	}
	if runner.cmd != cmd {
		t.Fatalf("command: got %#v want %#v", runner.cmd, cmd)
	}
}

func TestStartQEMUReturnsRunnerError(t *testing.T) {
	wantErr := errors.New("start qemu failed")
	_, err := StartQEMU(&qemuRunner{err: wantErr}, nil, &Plan{QEMUCommand: exec.Command("/bin/qemu")})
	if !errors.Is(err, wantErr) {
		t.Fatalf("runner error: got %v want %v", err, wantErr)
	}
}

type qemuRunner struct {
	cmd *exec.Cmd
	err error
}

func (r *qemuRunner) Start(cmd *exec.Cmd) (*executor.Process, error) {
	if r.err != nil {
		return nil, r.err
	}
	r.cmd = cmd
	return (&executortest.Process{OverrideName: "qemu-system-x86_64"}).Process(), nil
}
