package launch

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/executor"
	"github.com/shazow/agentspace/virtie/internal/executor/executortest"
	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestWaitForegroundRunsSSHAndRemovesRestoredState(t *testing.T) {
	plan := testForegroundPlan()
	plan.Options.SSH = true
	plan.ResumeState = &SuspendState{VMStatePath: "state"}
	var ranSSH bool
	var removed bool

	err := WaitForeground(context.Background(), ForegroundWait{
		Plan: plan,
		RunSSH: func(context.Context) error {
			ranSSH = true
			return nil
		},
		RemoveRestored: func(got *Plan) error {
			if got != plan {
				t.Fatalf("remove plan: got %#v want %#v", got, plan)
			}
			removed = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("wait foreground: %v", err)
	}
	if !ranSSH || !removed {
		t.Fatalf("expected ssh and remove callbacks: ranSSH=%v removed=%v", ranSSH, removed)
	}
}

func TestWaitForegroundStartsFeaturesBeforeSSH(t *testing.T) {
	plan := testForegroundPlan()
	plan.Options.SSH = true
	var events []string

	err := WaitForeground(context.Background(), ForegroundWait{
		Plan: plan,
		StartFeatures: func(context.Context) {
			events = append(events, "features")
		},
		RunSSH: func(context.Context) error {
			events = append(events, "ssh")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("wait foreground: %v", err)
	}
	if got, want := events, []string{"features", "ssh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %v want %v", got, want)
	}
}

func TestWaitForegroundHeadlessPrintsHintAndWaitsVM(t *testing.T) {
	plan := testForegroundPlan()
	runtime := &recordingForegroundWatchers{}
	processes := &foregroundProcessFixture{qemu: (&executortest.Process{OverrideName: "qemu"}).Process()}
	var output bytes.Buffer
	var waited bool

	err := WaitForeground(context.Background(), ForegroundWait{
		Plan:        plan,
		QEMU:        processes.qemu,
		Output:      &output,
		SetWatchers: runtime.SetWatchers,
		VMWatchers:  processes.VMWatchers,
		WaitVM: func(ctx context.Context, qemu *executor.Process, watchers executor.Group) error {
			waited = true
			if qemu != processes.qemu {
				t.Fatalf("qemu: got %#v want %#v", qemu, processes.qemu)
			}
			if watchers.Len() != 1 {
				t.Fatalf("expected one watcher, got %d", watchers.Len())
			}
			return nil
		},
		RemoveRestored: func(*Plan) error {
			t.Fatal("unexpected restored state removal")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("wait foreground: %v", err)
	}
	if !waited {
		t.Fatalf("expected vm wait callback")
	}
	if runtime.watchers.Len() != 1 {
		t.Fatalf("expected runtime watchers to be set")
	}
	if got, want := output.String(), "connect with: /bin/ssh agent@vsock/10\n"; got != want {
		t.Fatalf("hint output: got %q want %q", got, want)
	}
}

func TestWaitForegroundHeadlessRemovesRestoredStateBeforeVMWait(t *testing.T) {
	plan := testForegroundPlan()
	plan.ResumeState = &SuspendState{VMStatePath: "state"}
	processes := &foregroundProcessFixture{qemu: (&executortest.Process{OverrideName: "qemu"}).Process()}
	order := []string{}

	err := WaitForeground(context.Background(), ForegroundWait{
		Plan:       plan,
		QEMU:       processes.qemu,
		VMWatchers: processes.VMWatchers,
		WaitVM: func(context.Context, *executor.Process, executor.Group) error {
			order = append(order, "wait")
			return nil
		},
		RemoveRestored: func(*Plan) error {
			order = append(order, "remove")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("wait foreground: %v", err)
	}
	if got, want := order, []string{"remove", "wait"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("order: got %v want %v", got, want)
	}
}

func testForegroundPlan() *Plan {
	return &Plan{
		Manifest: &manifest.Manifest{
			SSH: manifest.SSH{
				Argv: []string{"/bin/ssh"},
				User: "agent",
			},
		},
		CID: 10,
	}
}

type recordingForegroundWatchers struct {
	watchers executor.Group
}

func (r *recordingForegroundWatchers) SetWatchers(watchers executor.Group) {
	r.watchers = watchers
}

type foregroundProcessFixture struct {
	qemu *executor.Process
}

func (p *foregroundProcessFixture) VMWatchers() executor.Group {
	return executor.NewGroup((&executortest.Process{OverrideName: "run"}).Process())
}
