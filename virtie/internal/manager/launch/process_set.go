package launch

import (
	"context"
	"errors"
	"time"

	"github.com/shazow/agentspace/virtie/internal/executor"
)

type ProcessSet struct {
	group executor.Group
	qemu  *executor.Process
	tasks taskGroup
}

func NewProcessSet() *ProcessSet {
	return &ProcessSet{group: executor.NewGroup()}
}

func (p *ProcessSet) Add(processes ...*executor.Process) {
	p.group.Add(processes...)
}

func (p *ProcessSet) AddGroup(group executor.Group) {
	p.group.Add(group.Processes()...)
}

func (p *ProcessSet) SetQEMU(process *executor.Process) {
	p.qemu = process
	p.Add(process)
}

func (p *ProcessSet) QEMU() *executor.Process {
	return p.qemu
}

func (p *ProcessSet) Remove(process *executor.Process) bool {
	return p.group.Remove(process)
}

func (p *ProcessSet) Watchers() executor.Group {
	return p.group.Snapshot()
}

func (p *ProcessSet) VMWatchers() executor.Group {
	watchers := p.Watchers()
	watchers.Remove(p.qemu)
	return watchers
}

func (p *ProcessSet) StartTasks(ctx context.Context, tasks ...func(context.Context) error) {
	var started taskGroup
	for _, task := range tasks {
		started.Add(startTask(ctx, task))
	}
	p.tasks = started
}

func (p *ProcessSet) Close(delay time.Duration) error {
	return errors.Join(p.tasks.Stop(), p.group.StopAll(delay))
}
