package executor

import (
	"errors"
	"reflect"
	"strings"
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

func TestFirstExit(t *testing.T) {
	first := Wrap(&FakeProcess{FakeName: "first"})
	secondProcess := &FakeProcess{FakeName: "second"}
	second := Wrap(secondProcess)
	secondProcess.Complete(errors.New("second failed"))
	<-second.Done()

	process, err, ok := FirstExit(first, second)
	if !ok || process != second || err == nil || err.Error() != "second failed" {
		t.Fatalf("first exit: process=%v ok=%v err=%v", process, ok, err)
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

func TestStopAllStopsInReverseOrder(t *testing.T) {
	firstProcess := &FakeProcess{FakeName: "first"}
	secondProcess := &FakeProcess{FakeName: "second"}
	first := Wrap(firstProcess)
	second := Wrap(secondProcess)

	if err := StopAll([]*Process{first, second}, time.Second); err != nil {
		t.Fatalf("stop all: %v", err)
	}
	if got, want := []FakeProcessEventKind{secondProcess.EventKinds()[0], firstProcess.EventKinds()[0]}, []FakeProcessEventKind{FakeProcessSignal, FakeProcessSignal}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events: got %v want %v", got, want)
	}
	if secondProcess.Events()[0].Sequence >= firstProcess.Events()[0].Sequence {
		t.Fatalf("expected second to stop before first: second=%d first=%d", secondProcess.Events()[0].Sequence, firstProcess.Events()[0].Sequence)
	}
}
