package runtime

import (
	"context"
	"log/slog"

	"github.com/shazow/agentspace/virtie/internal/manager/control"
)

type ControlServer struct {
	server *control.Server
}

func StartControl(ctx context.Context, socketPath string, handler any, logger *slog.Logger) (*ControlServer, error) {
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
	server := &control.Server{Handler: router, Logger: logger}
	controlServer := &ControlServer{server: server}
	go func() {
		if err := server.Serve(listener); err != nil && ctx.Err() == nil && logger != nil {
			logger.Info("control socket stopped", "err", err)
		}
	}()
	return controlServer, nil
}

func (s *ControlServer) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Close()
}
