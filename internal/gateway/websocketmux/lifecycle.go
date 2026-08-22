package websocketmux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

func (h *Handler) BeginDrain() {
	h.draining.Store(true)
}

func (h *Handler) Draining() bool {
	return h.draining.Load()
}

func (h *Handler) ActiveSessions() int {
	return len(h.limit)
}

func (h *Handler) logf(requestID, format string, values ...any) {
	if h.config.Logger != nil {
		h.config.Logger.Info(fmt.Sprintf(format, values...), "request_id", requestID)
	}
}

func Serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: defaultKeepAliveInterval,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-done:
		}
	}()
	err := server.Serve(listener)
	close(done)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil //nolint:nilerr // Server closure after context cancellation is a successful shutdown.
	}
	return err
}
