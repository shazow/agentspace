package launch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shazow/agentspace/virtie/internal/manifest"
)

func TestProvisionGuestWritesFilesWaitsForSSHReadyAndEnablesWriteBack(t *testing.T) {
	plan := &Plan{
		Manifest: &manifest.Manifest{},
		Paths: RuntimePaths{
			SSHReadySocket: "ssh-ready.sock",
		},
	}
	stats := &fakeGuestProvisionStats{}
	var wrote bool
	var waitedSocket string

	writeBack, err := ProvisionGuest(context.Background(), GuestProvision{
		Plan:  plan,
		Stats: stats,
		Now: func() time.Time {
			return time.Unix(10, 0)
		},
		WriteFiles: func(context.Context) error {
			wrote = true
			return nil
		},
		WaitSSHReady: func(ctx context.Context, socketPath string) error {
			waitedSocket = socketPath
			return nil
		},
	})
	if err != nil {
		t.Fatalf("provision guest: %v", err)
	}
	if !writeBack {
		t.Fatalf("expected write-back to be enabled")
	}
	if !wrote {
		t.Fatalf("expected guest files to be written")
	}
	if waitedSocket != "ssh-ready.sock" {
		t.Fatalf("ssh ready socket: got %q want ssh-ready.sock", waitedSocket)
	}
	if got, want := stats.filesReady, 1; got != want {
		t.Fatalf("files ready marks: got %d want %d", got, want)
	}
	if got, want := stats.sshReady, 1; got != want {
		t.Fatalf("ssh ready marks: got %d want %d", got, want)
	}
}

func TestProvisionGuestSkipsForResumeState(t *testing.T) {
	var wrote bool
	writeBack, err := ProvisionGuest(context.Background(), GuestProvision{
		Plan: &Plan{
			Manifest:    &manifest.Manifest{},
			ResumeState: &SuspendState{VMStatePath: "state"},
		},
		WriteFiles: func(context.Context) error {
			wrote = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("provision guest: %v", err)
	}
	if writeBack {
		t.Fatalf("did not expect write-back for resumed VM")
	}
	if wrote {
		t.Fatalf("did not expect guest files to be written")
	}
}

func TestProvisionGuestReturnsWriteError(t *testing.T) {
	wantErr := errors.New("write failed")
	writeBack, err := ProvisionGuest(context.Background(), GuestProvision{
		Plan: &Plan{Manifest: &manifest.Manifest{}},
		WriteFiles: func(context.Context) error {
			return wantErr
		},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("write err: got %v want %v", err, wantErr)
	}
	if writeBack {
		t.Fatalf("did not expect write-back after write error")
	}
}

type fakeGuestProvisionStats struct {
	filesReady int
	sshReady   int
}

func (s *fakeGuestProvisionStats) MarkFilesReady(time.Time) {
	s.filesReady++
}

func (s *fakeGuestProvisionStats) MarkSSHReady(time.Time) {
	s.sshReady++
}
