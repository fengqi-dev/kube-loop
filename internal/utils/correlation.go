package utils

import (
	"context"

	"github.com/google/uuid"
)

type correlationKey struct{}

// NewCorrelationID returns a canonical random correlation identifier.
func NewCorrelationID() string { return uuid.NewString() }

// ValidCorrelationID reports whether value is a canonical UUID.
func ValidCorrelationID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

// WithCorrelationID returns a child context carrying id. Invalid IDs are
// ignored so untrusted transport metadata never enters logs without
// validation.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !ValidCorrelationID(id) {
		return ctx
	}
	return context.WithValue(ctx, correlationKey{}, id)
}

// CorrelationID returns the validated correlation identifier carried by ctx.
func CorrelationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(correlationKey{}).(string)
	if !ValidCorrelationID(id) {
		return ""
	}
	return id
}

// EnsureCorrelationID returns a context carrying a correlation identifier and
// that ID.
func EnsureCorrelationID(ctx context.Context) (context.Context, string) {
	if id := CorrelationID(ctx); id != "" {
		return ctx, id
	}
	id := NewCorrelationID()
	return WithCorrelationID(ctx, id), id
}
