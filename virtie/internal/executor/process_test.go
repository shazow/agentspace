package executor

import (
	"errors"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessCachesWaitResult(t *testing.T) {
	handle := &FakeProcess{FakeName: "worker"}
	process := Wrap(handle)
	handle.Complete(errors.New("done"))

	for i := 0; i < 2; i++ {
		err := process.Wait()
		if err == nil || err.Error() != "done" {
			t.Fatalf("wait %d: got %v", i, err)
		}
	}
}

func TestProcessPollExit(t *testing.T) {
	handle := &FakeProcess{FakeName: "worker"}
	process := Wrap(handle)
	if exited, err := process.PollExit(); exited || err != nil {
		t.Fatalf("poll before exit: exited=%v err=%v", exited, err)
	}

	handle.Complete(errors.New("failed"))
	<-process.Done()

	exited, err := process.PollExit()
	if !exited || err == nil || err.Error() != "failed" {
		t.Fatalf("poll after exit: exited=%v err=%v", exited, err)
	}
}

func TestProcessStopUsesShutdownCallback(t *testing.T) {
	handle := &FakeProcess{FakeName: "worker"}
	process := Wrap(handle)
	called := false
	process.SetShutdown(func() error {
		called = true
		handle.Complete(nil)
		return nil
	})

	if err := process.Stop(time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !called {
		t.Fatal("expected shutdown callback")
	}
	if signals := handle.Signals(); len(signals) != 0 {
		t.Fatalf("unexpected signals: %v", signals)
	}
}

func TestProcessStopSignalsThenKills(t *testing.T) {
	handle := &FakeProcess{FakeName: "worker", IgnoreSignals: true}
	process := Wrap(handle)

	if err := process.Stop(time.Millisecond); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got, want := handle.EventKinds(), []FakeProcessEventKind{FakeProcessSignal, FakeProcessKill}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %v want %v", got, want)
	}
}

func TestProcessStopReportsShutdownAndSignalErrors(t *testing.T) {
	handle := &FakeProcess{FakeName: "worker", SignalErr: errors.New("signal failed")}
	process := Wrap(handle)
	process.SetShutdown(func() error {
		return errors.New("shutdown failed")
	})

	err := process.Stop(time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "shutdown worker") || !strings.Contains(err.Error(), "stop worker") {
		t.Fatalf("expected joined shutdown and stop error, got %v", err)
	}
}

func TestProcessKillAndWait(t *testing.T) {
	handle := &FakeProcess{FakeName: "worker"}
	process := Wrap(handle)

	if err := process.KillAndWait(); err != nil {
		t.Fatalf("kill and wait: %v", err)
	}
	if got, want := handle.EventKinds(), []FakeProcessEventKind{FakeProcessKill}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %v want %v", got, want)
	}
}

func TestSignalProcessGroupIgnoresMissingProcess(t *testing.T) {
	if err := SignalProcessGroup(999999, syscall.SIGTERM); err != nil {
		t.Fatalf("signal missing process: %v", err)
	}
}

func TestSignalProcessGroupIgnoresNonPositivePID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if err := SignalProcessGroup(pid, syscall.SIGTERM); err != nil {
			t.Fatalf("signal pid %d: %v", pid, err)
		}
	}
}
