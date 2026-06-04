package executor

import (
	"os"
	"sync"
	"sync/atomic"
)

// FakeProcessEventKind identifies a lifecycle event recorded by FakeProcess.
type FakeProcessEventKind string

const (
	FakeProcessSignal FakeProcessEventKind = "signal"
	FakeProcessKill   FakeProcessEventKind = "kill"
)

var fakeProcessEventSequence atomic.Int64

// FakeProcessEvent records a signal or kill operation on a FakeProcess.
type FakeProcessEvent struct {
	Kind     FakeProcessEventKind
	Signal   os.Signal
	Sequence int64
}

// FakeProcess is a test implementation of RunningProcess.
//
// The zero value is a running process named "fake" with PID 1. Set Exited to
// true to make Wait return immediately with WaitErr.
type FakeProcess struct {
	FakeName string
	FakePID  int

	Exited  bool
	WaitErr error

	SignalErr     error
	KillErr       error
	IgnoreSignals bool

	OnSignal func(os.Signal)
	OnKill   func()

	mu        sync.Mutex
	done      chan struct{}
	completed bool
	waitErr   error
	signals   []os.Signal
	events    []FakeProcessEvent
}

var _ RunningProcess = (*FakeProcess)(nil)

// Process wraps p as an executor Process.
func (p *FakeProcess) Process() *Process {
	return Wrap(p)
}

// Complete marks p as exited and unblocks Wait.
func (p *FakeProcess) Complete(err error) {
	if p == nil {
		return
	}
	p.complete(err)
}

// Done returns a channel closed when p exits.
func (p *FakeProcess) Done() <-chan struct{} {
	if p == nil {
		return closedDone()
	}
	p.mu.Lock()
	done := p.doneLocked()
	p.mu.Unlock()
	return done
}

// Wait blocks until p exits and returns its completion error.
func (p *FakeProcess) Wait() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	done := p.doneLocked()
	p.mu.Unlock()
	<-done

	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}

// Signal records sig, invokes OnSignal, and completes p unless IgnoreSignals is set.
func (p *FakeProcess) Signal(sig os.Signal) error {
	if p == nil {
		return nil
	}
	p.recordEvent(FakeProcessSignal, sig)
	if p.OnSignal != nil {
		p.OnSignal(sig)
	}
	if p.SignalErr != nil {
		return p.SignalErr
	}
	if !p.IgnoreSignals {
		p.complete(p.WaitErr)
	}
	return nil
}

// Kill records a kill event, invokes OnKill, and completes p.
func (p *FakeProcess) Kill() error {
	if p == nil {
		return nil
	}
	p.recordEvent(FakeProcessKill, nil)
	if p.OnKill != nil {
		p.OnKill()
	}
	if p.KillErr != nil {
		return p.KillErr
	}
	p.complete(p.WaitErr)
	return nil
}

// PID returns FakePID, or 1 when FakePID is zero.
func (p *FakeProcess) PID() int {
	if p == nil {
		return 0
	}
	if p.FakePID != 0 {
		return p.FakePID
	}
	return 1
}

// Name returns FakeName, or "fake" when FakeName is empty.
func (p *FakeProcess) Name() string {
	if p == nil {
		return ""
	}
	if p.FakeName != "" {
		return p.FakeName
	}
	return "fake"
}

// Signals returns the signals recorded by Signal.
func (p *FakeProcess) Signals() []os.Signal {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]os.Signal(nil), p.signals...)
}

// Events returns signal and kill events in the order p observed them.
func (p *FakeProcess) Events() []FakeProcessEvent {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]FakeProcessEvent(nil), p.events...)
}

// EventKinds returns the kinds of events recorded by p.
func (p *FakeProcess) EventKinds() []FakeProcessEventKind {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	kinds := make([]FakeProcessEventKind, 0, len(p.events))
	for _, event := range p.events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func (p *FakeProcess) complete(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.completed {
		return
	}
	done := p.doneLocked()
	p.waitErr = err
	p.completed = true
	close(done)
}

func (p *FakeProcess) doneLocked() chan struct{} {
	if p.done == nil {
		p.done = make(chan struct{})
		if p.Exited {
			p.waitErr = p.WaitErr
			p.completed = true
			close(p.done)
		}
	}
	return p.done
}

func (p *FakeProcess) recordEvent(kind FakeProcessEventKind, sig os.Signal) {
	event := FakeProcessEvent{
		Kind:     kind,
		Signal:   sig,
		Sequence: fakeProcessEventSequence.Add(1),
	}
	p.mu.Lock()
	if kind == FakeProcessSignal {
		p.signals = append(p.signals, sig)
	}
	p.events = append(p.events, event)
	p.mu.Unlock()
}
