package correlation

import (
	"context"
	"testing"
)

func TestEnsurePreservesValidID(t *testing.T) {
	t.Parallel()
	const id = "11111111-1111-4111-8111-111111111111"
	ctx, result := Ensure(WithID(t.Context(), id))
	if result != id || ID(ctx) != id {
		t.Fatalf("correlation ID = %q, context ID = %q", result, ID(ctx))
	}
}

func TestEnsureGeneratesCanonicalID(t *testing.T) {
	t.Parallel()
	ctx, id := Ensure(context.Background())
	if !Valid(id) || ID(ctx) != id {
		t.Fatalf("generated correlation ID = %q, context ID = %q", id, ID(ctx))
	}
}

func TestWithIDIgnoresInvalidID(t *testing.T) {
	t.Parallel()
	ctx := WithID(t.Context(), "not-a-uuid")
	if id := ID(ctx); id != "" {
		t.Fatalf("invalid correlation ID was stored: %q", id)
	}
}
