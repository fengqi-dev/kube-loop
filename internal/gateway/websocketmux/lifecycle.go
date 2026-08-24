package websocketmux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/correlation"
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

func (h *Handler) logf(ctx context.Context, requestID, format string, values ...any) {
	if h.config.Logger != nil {
		h.config.Logger.InfoContext(
			ctx,
			fmt.Sprintf(format, values...),
			"operation", "gateway.websocket.session",
			"outcome", "failure",
			"correlation_id", correlation.ID(ctx),
			"request_id", requestID,
		)
	}
}

func (h *Handler) logSession(
	ctx context.Context,
	message, operation, outcome string,
	identity Identity,
	attributes ...any,
) {
	if h.config.Logger == nil {
		return
	}
	arguments := []any{
		"operation", operation,
		"outcome", outcome,
		"correlation_id", correlation.ID(ctx),
		"request_id", identity.RequestID,
		"session_id", identity.SessionID,
		"session_generation", identity.SessionGeneration,
		"ticket_id", identity.TicketID,
	}
	arguments = append(arguments, attributes...)
	h.config.Logger.InfoContext(ctx, message, arguments...)
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
