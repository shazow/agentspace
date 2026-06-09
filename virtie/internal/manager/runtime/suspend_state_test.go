package runtime

import "testing"

func TestSavedSuspendState(t *testing.T) {
	state := NewSavedSuspendState()
	if state.Saved() {
		t.Fatal("new saved suspend state should be unsaved")
	}
	state.MarkSaved()
	if !state.Saved() {
		t.Fatal("saved suspend state should be saved")
	}
}

func TestNilSavedSuspendStateUnsaved(t *testing.T) {
	var state *SavedSuspendState
	if state.Saved() {
		t.Fatal("nil saved suspend state should be unsaved")
	}
}
