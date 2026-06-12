package runtime

import (
	"context"
	"log/slog"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

func StartControl(ctx context.Context, socketPath string, handler any, logger *slog.Logger) (*control.Server, error) {
	if socketPath == "" {
		return nil, nil
	}
	listener, err := control.Listen(socketPath)
	if err != nil {
		return nil, err
	}
	router, err := control.NewRuntimeRouter(handler)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	server, err := control.NewServer(router)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	go func() {
		if err := server.Serve(listener); err != nil && ctx.Err() == nil && logger != nil {
			logger.Info("control socket stopped", "err", err)
		}
	}()
	return server, nil
}
