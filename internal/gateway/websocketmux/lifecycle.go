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
	stopClose := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stopClose()
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return nil
	}
	return err
}
