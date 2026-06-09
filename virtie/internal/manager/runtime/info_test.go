package runtime

import (
	"context"
	"errors"
	"testing"
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
