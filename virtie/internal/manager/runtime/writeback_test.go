package runtime

import "testing"

func TestWriteBackState(t *testing.T) {
	state := NewWriteBackState()
	if state.Enabled() {
		t.Fatal("new write-back state should be disabled")
	}
	state.Enable()
	if !state.Enabled() {
		t.Fatal("write-back state should be enabled")
	}
}

func TestNilWriteBackStateDisabled(t *testing.T) {
	var state *WriteBackState
	if state.Enabled() {
		t.Fatal("nil write-back state should be disabled")
	}
}
