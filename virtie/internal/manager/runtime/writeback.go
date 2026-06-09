package runtime

import "sync"

type WriteBackState struct {
	mu      sync.Mutex
	enabled bool
}

func NewWriteBackState() *WriteBackState {
	return &WriteBackState{}
}

func (s *WriteBackState) Enable() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = true
}

func (s *WriteBackState) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}
