package runtime

import (
	"context"
	"fmt"
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
	core, ok := handler.(control.RuntimeCore)
	if !ok {
		_ = listener.Close()
		return nil, fmt.Errorf("runtime core handler is required")
	}
	options := []control.RouterOption{}
	if suspend, ok := handler.(control.RuntimeSuspend); ok {
		options = append(options, control.WithSuspend(suspend))
	}
	if hotplug, ok := handler.(control.RuntimeHotplug); ok {
		options = append(options, control.WithHotplug(hotplug))
	}
	if balloon, ok := handler.(control.RuntimeBalloon); ok {
		options = append(options, control.WithBalloon(balloon))
	}
	router, err := control.NewRouter(core, options...)
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
