package utils

import (
	"context"
	"testing"
)

func TestEnsureCorrelationIDPreservesValidID(t *testing.T) {
	t.Parallel()
	const id = "11111111-1111-4111-8111-111111111111"
	ctx, result := EnsureCorrelationID(WithCorrelationID(t.Context(), id))
	if result != id || CorrelationID(ctx) != id {
		t.Fatalf("correlation ID = %q, context ID = %q", result, CorrelationID(ctx))
	}
}

func TestEnsureCorrelationIDGeneratesCanonicalID(t *testing.T) {
	t.Parallel()
	ctx, id := EnsureCorrelationID(context.Background())
	if !ValidCorrelationID(id) || CorrelationID(ctx) != id {
		t.Fatalf("generated correlation ID = %q, context ID = %q", id, CorrelationID(ctx))
	}
}

func TestWithCorrelationIDIgnoresInvalidID(t *testing.T) {
	t.Parallel()
	ctx := WithCorrelationID(t.Context(), "not-a-uuid")
	if id := CorrelationID(ctx); id != "" {
		t.Fatalf("invalid correlation ID was stored: %q", id)
	}
}
