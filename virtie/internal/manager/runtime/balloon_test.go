package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	rawQMP "github.com/digitalocean/go-qemu/qmp/raw"
	"github.com/shazow/agentspace/virtie/internal/balloontypes"
	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type fakeBalloonQMP struct {
	err error
}

func (q fakeBalloonQMP) WithRaw(time.Duration, func(*rawQMP.Monitor) error) error {
	return q.err
}

func TestBalloonRequiresConfiguredDevice(t *testing.T) {
	_, err := Balloon(context.Background(), nil, fakeBalloonQMP{}, time.Second, control.BalloonRequest{})
	if !errors.Is(err, ErrBalloonNotConfigured) {
		t.Fatalf("error: got %v want %v", err, ErrBalloonNotConfigured)
	}
}

func TestBalloonPropagatesQMPError(t *testing.T) {
	qmpErr := errors.New("qmp failed")
	_, err := Balloon(context.Background(), &balloontypes.Device{}, fakeBalloonQMP{err: qmpErr}, time.Second, control.BalloonRequest{})
	if !errors.Is(err, qmpErr) {
		t.Fatalf("error: got %v want %v", err, qmpErr)
	}
}
