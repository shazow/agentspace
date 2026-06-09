package runtime

import (
	"context"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/executor/executortest"
)

func TestProcessSetWatchersAndVMWatchers(t *testing.T) {
	qemu := (&executortest.Process{}).Process()
	run := (&executortest.Process{}).Process()
	processes := NewProcessSet()
	processes.SetQEMU(qemu)
	processes.Add(run)

	watchers := processes.Watchers()
	if got, want := len(watchers.Processes()), 2; got != want {
		t.Fatalf("unexpected watcher count: got %d want %d", got, want)
	}
	vmWatcherGroup := processes.VMWatchers()
	vmWatchers := vmWatcherGroup.Processes()
	if got, want := len(vmWatchers), 1; got != want {
		t.Fatalf("unexpected vm watcher count: got %d want %d", got, want)
	}
	if vmWatchers[0] != run {
		t.Fatalf("vm watchers should exclude qemu process")
	}
}

func TestProcessSetCloseStopsFeaturesBeforeProcesses(t *testing.T) {
	var order []string
	processes := NewProcessSet()
	process := (&executortest.Process{}).Process()
	process.SetShutdown(func() error {
		order = append(order, "process")
		return nil
	})
	processes.Add(process)

	var features TaskGroup
	features.Add(StartTask(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		order = append(order, "feature")
		return nil
	}))
	processes.SetFeatures(features)

	if err := processes.Close(0); err != nil {
		t.Fatalf("close process set: %v", err)
	}
	if got, want := order, []string{"feature", "process"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected close order: got %#v want %#v", got, want)
	}
}
