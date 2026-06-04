package executortest

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestProcessWaitBlocksUntilComplete(t *testing.T) {
	process := &Process{NameValue: "worker"}

	select {
	case <-process.Done():
		t.Fatal("process exited before completion")
	default:
	}

	process.Complete(errors.New("done"))

	if err := process.Wait(); err == nil || err.Error() != "done" {
		t.Fatalf("wait: %v", err)
	}
}

func TestProcessExitedWaitsImmediately(t *testing.T) {
	process := &Process{Exited: true, WaitErr: errors.New("failed")}

	if err := process.Wait(); err == nil || err.Error() != "failed" {
		t.Fatalf("wait: %v", err)
	}
}

func TestProcessProcessCachesWaitResult(t *testing.T) {
	handle := &Process{NameValue: "worker"}
	process := handle.Process()
	handle.Complete(errors.New("done"))

	for i := 0; i < 2; i++ {
		err := process.Wait()
		if err == nil || err.Error() != "done" {
			t.Fatalf("wait %d: %v", i, err)
		}
	}
}

func TestSignalCompletesByDefault(t *testing.T) {
	process := &Process{WaitErr: errors.New("stopped")}

	if err := process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal: %v", err)
	}
	if err := process.Wait(); err == nil || err.Error() != "stopped" {
		t.Fatalf("wait: %v", err)
	}
	if got, want := process.Signals(), []os.Signal{os.Interrupt}; !reflect.DeepEqual(got, want) {
		t.Fatalf("signals: got %v want %v", got, want)
	}
}

func TestProcessIgnoreSignalsAllowsKillEscalation(t *testing.T) {
	handle := &Process{IgnoreSignals: true}
	process := handle.Process()

	if err := process.Stop(time.Millisecond); err != nil {
		t.Fatalf("stop: %v", err)
	}

	events := handle.Events()
	if got, want := len(events), 2; got != want {
		t.Fatalf("events: got %d want %d (%v)", got, want, events)
	}
	if events[0].Kind != Signal || events[1].Kind != Kill {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestProcessRecordsErrorsAndEvents(t *testing.T) {
	process := &Process{
		SignalErr: errors.New("signal failed"),
		KillErr:   errors.New("kill failed"),
	}

	if err := process.Signal(os.Interrupt); err == nil || err.Error() != "signal failed" {
		t.Fatalf("signal error: %v", err)
	}
	if err := process.Kill(); err == nil || err.Error() != "kill failed" {
		t.Fatalf("kill error: %v", err)
	}

	events := process.Events()
	if got, want := len(events), 2; got != want {
		t.Fatalf("events: got %d want %d (%v)", got, want, events)
	}
	if events[0].Kind != Signal || events[1].Kind != Kill {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestEventSequenceOrdersProcesses(t *testing.T) {
	first := &Process{NameValue: "first"}
	second := &Process{NameValue: "second"}

	if err := second.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal second: %v", err)
	}
	if err := first.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal first: %v", err)
	}

	if second.Events()[0].Sequence >= first.Events()[0].Sequence {
		t.Fatalf("expected second event before first: second=%d first=%d", second.Events()[0].Sequence, first.Events()[0].Sequence)
	}
}
