package executor

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProcessCachesWaitResult(t *testing.T) {
	handle := newProcessTestHandle("worker")
	process := Wrap(handle)
	handle.complete(errors.New("done"))

	for i := 0; i < 2; i++ {
		err := process.Wait()
		if err == nil || err.Error() != "done" {
			t.Fatalf("wait %d: got %v", i, err)
		}
	}
}

func TestProcessPollExit(t *testing.T) {
	handle := newProcessTestHandle("worker")
	process := Wrap(handle)
	if exited, err := process.PollExit(); exited || err != nil {
		t.Fatalf("poll before exit: exited=%v err=%v", exited, err)
	}

	handle.complete(errors.New("failed"))
	<-process.Done()

	exited, err := process.PollExit()
	if !exited || err == nil || err.Error() != "failed" {
		t.Fatalf("poll after exit: exited=%v err=%v", exited, err)
	}
}

func TestFirstExit(t *testing.T) {
	first := Wrap(newProcessTestHandle("first"))
	secondProcess := newProcessTestHandle("second")
	second := Wrap(secondProcess)
	secondProcess.complete(errors.New("second failed"))
	<-second.Done()

	process, err, ok := FirstExit(first, second)
	if !ok || process != second || err == nil || err.Error() != "second failed" {
		t.Fatalf("first exit: process=%v ok=%v err=%v", process, ok, err)
	}
}

func TestProcessStopUsesShutdownCallback(t *testing.T) {
	handle := newProcessTestHandle("worker")
	process := Wrap(handle)
	called := false
	process.SetShutdown(func() error {
		called = true
		handle.complete(nil)
		return nil
	})

	if err := process.Stop(time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !called {
		t.Fatal("expected shutdown callback")
	}
	if len(handle.signals) != 0 {
		t.Fatalf("unexpected signals: %v", handle.signals)
	}
}

func TestProcessStopSignalsThenKills(t *testing.T) {
	handle := newProcessTestHandle("worker")
	handle.ignoreSignals = true
	process := Wrap(handle)

	if err := process.Stop(time.Millisecond); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got, want := handle.signals, []string{"signal", "kill"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("signals: got %v want %v", got, want)
	}
}

func TestProcessStopReportsShutdownAndSignalErrors(t *testing.T) {
	handle := newProcessTestHandle("worker")
	handle.signalErr = errors.New("signal failed")
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
	handle := newProcessTestHandle("worker")
	process := Wrap(handle)

	if err := process.KillAndWait(); err != nil {
		t.Fatalf("kill and wait: %v", err)
	}
	if got, want := handle.signals, []string{"kill"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("signals: got %v want %v", got, want)
	}
}

func TestStopAllStopsInReverseOrder(t *testing.T) {
	firstProcess := newProcessTestHandle("first")
	secondProcess := newProcessTestHandle("second")
	first := Wrap(firstProcess)
	second := Wrap(secondProcess)

	if err := StopAll([]*Process{first, second}, time.Second); err != nil {
		t.Fatalf("stop all: %v", err)
	}
	if got, want := []string{secondProcess.signals[0], firstProcess.signals[0]}, []string{"signal", "signal"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("signals: got %v want %v", got, want)
	}
	if secondProcess.signalIndex >= firstProcess.signalIndex {
		t.Fatalf("expected second to stop before first: second=%d first=%d", secondProcess.signalIndex, firstProcess.signalIndex)
	}
}

type processTestHandle struct {
	name          string
	done          chan error
	once          sync.Once
	mu            sync.Mutex
	signals       []string
	signalIndex   int
	ignoreSignals bool
	signalErr     error
}

var processSignalCounter int

func newProcessTestHandle(name string) *processTestHandle {
	return &processTestHandle{name: name, done: make(chan error, 1)}
}

func (p *processTestHandle) Wait() error {
	err, ok := <-p.done
	if !ok {
		return nil
	}
	return err
}

func (p *processTestHandle) Signal(sig os.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	processSignalCounter++
	p.signalIndex = processSignalCounter
	p.signals = append(p.signals, "signal")
	if p.signalErr != nil {
		return p.signalErr
	}
	if !p.ignoreSignals {
		p.complete(nil)
	}
	return nil
}

func (p *processTestHandle) Kill() error {
	p.mu.Lock()
	processSignalCounter++
	p.signalIndex = processSignalCounter
	p.signals = append(p.signals, "kill")
	p.mu.Unlock()
	p.complete(nil)
	return nil
}

func (p *processTestHandle) PID() int {
	return 1
}

func (p *processTestHandle) Name() string {
	return p.name
}

func (p *processTestHandle) complete(err error) {
	p.once.Do(func() {
		select {
		case p.done <- err:
		default:
		}
		close(p.done)
	})
}
