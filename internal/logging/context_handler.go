package logging

import (
	"context"
	"log/slog"

	"github.com/fengqi-dev/kube-loop/internal/correlation"
)

// WithContext decorates handler with request-scoped correlation attributes.
func WithContext(handler slog.Handler) slog.Handler {
	return &contextHandler{handler: handler}
}

type contextHandler struct {
	handler slog.Handler
}

func (handler *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.handler.Enabled(ctx, level)
}

func (handler *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	hasCorrelationID := false
	record.Attrs(func(attribute slog.Attr) bool {
		if attribute.Key == "correlation_id" {
			hasCorrelationID = true
			return false
		}
		return true
	})
	if id := correlation.ID(ctx); id != "" && !hasCorrelationID {
		record.AddAttrs(slog.String("correlation_id", id))
	}
	return handler.handler.Handle(ctx, record)
}

func (handler *contextHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	return &contextHandler{handler: handler.handler.WithAttrs(attributes)}
}

func (handler *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{handler: handler.handler.WithGroup(name)}
}
