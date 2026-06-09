package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type fakeInfoCollector struct {
	info GuestInfo
	err  error
}

func (c fakeInfoCollector) CollectInfo(context.Context) (GuestInfo, error) {
	return c.info, c.err
}

func TestInfoReturnsCollectedProcessList(t *testing.T) {
	resp, err := Info(context.Background(), fakeInfoCollector{info: GuestInfo{ProcessList: "USER COMMAND\nroot init"}})
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if resp.ProcessList != "USER COMMAND\nroot init" {
		t.Fatalf("process list: %q", resp.ProcessList)
	}
}

func TestInfoPropagatesCollectorError(t *testing.T) {
	collectErr := errors.New("guest agent failed")
	_, err := Info(context.Background(), fakeInfoCollector{err: collectErr})
	if !errors.Is(err, collectErr) {
		t.Fatalf("error: got %v want %v", err, collectErr)
	}
}

func TestControlInfoMapsCollectorErrorToFailedPrecondition(t *testing.T) {
	_, err := ControlInfo(context.Background(), fakeInfoCollector{err: errors.New("guest agent failed")})
	var rpcErr *control.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("error type: got %T", err)
	}
	if rpcErr.Code != control.ErrFailedPrecondition {
		t.Fatalf("code: got %s want %s", rpcErr.Code, control.ErrFailedPrecondition)
	}
}
