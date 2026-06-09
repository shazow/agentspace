package runtime

import (
	"sync"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type State struct {
	mu    sync.Mutex
	value control.RuntimeState
}

func NewState(initial control.RuntimeState) *State {
	return &State{value: initial}
}

func (s *State) Set(value control.RuntimeState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = value
}

func (s *State) Current() control.RuntimeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}
