package middleware

import (
	"context"

	"github.com/google/uuid"
)

const Header = "X-Kubeloop-Correlation-Id"

type contextKey struct{}

// New returns a canonical random correlation identifier.
func New() string { return uuid.NewString() }

// Valid reports whether value is a canonical UUID.
func Valid(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value
}

// WithID returns a child context carrying id. Invalid IDs are ignored so
// untrusted transport metadata never enters logs without validation.
func WithID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !Valid(id) {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, id)
}

// ID returns the validated correlation identifier carried by ctx.
func ID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(contextKey{}).(string)
	if !Valid(id) {
		return ""
	}
	return id
}

// Ensure returns a context carrying a correlation identifier and that ID.
func Ensure(ctx context.Context) (context.Context, string) {
	if id := ID(ctx); id != "" {
		return ctx, id
	}
	id := New()
	return WithID(ctx, id), id
}
