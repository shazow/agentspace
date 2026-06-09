package runtime

import "sync"

type SavedSuspendState struct {
	mu    sync.Mutex
	saved bool
}

func NewSavedSuspendState() *SavedSuspendState {
	return &SavedSuspendState{}
}

func (s *SavedSuspendState) MarkSaved() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = true
}

func (s *SavedSuspendState) Saved() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saved
}
