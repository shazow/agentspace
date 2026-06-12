package runtime

import (
	"context"
	"errors"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

var ErrSuspendNotReady = errors.New("suspend handler is not ready")

type SuspendRequester interface {
	RequestAndWait(context.Context) error
}

func MarkReady(state *State) {
	state.Set(control.RuntimeReady)
}

func Status(state *State, cid int, paths control.StatusPaths, stats *Stats) control.StatusResponse {
	return control.StatusResponse{
		State: state.Current(),
		CID:   cid,
		Paths: paths,
		Stats: ControlStats(stats),
	}
}

func QueueSuspend(ctx context.Context, state *State, requester SuspendRequester, savedSuspendExit func(error) bool) error {
	if requester == nil {
		return ErrSuspendNotReady
	}
	state.Set(control.RuntimeSuspending)
	err := requester.RequestAndWait(ctx)
	if err != nil && (savedSuspendExit == nil || !savedSuspendExit(err)) {
		return err
	}
	state.Set(control.RuntimeSuspended)
	return nil
}
