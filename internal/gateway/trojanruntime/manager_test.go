package trojanruntime

import (
	"context"
	"testing"
)

func TestManagerLifetimeEndsOnCloseInsteadOfParentCancellation(t *testing.T) {
	type contextKey struct{}
	parent, cancelParent := context.WithCancel(context.WithValue(t.Context(), contextKey{}, "value"))
	manager, err := NewManager(parent, Config{BinaryPath: "sing-box"})
	if err != nil {
		t.Fatal(err)
	}
	cancelParent()
	select {
	case <-manager.ctx.Done():
		t.Fatal("parent cancellation ended the Gateway forward runtime before drain")
	default:
	}
	if got := manager.ctx.Value(contextKey{}); got != "value" {
		t.Fatalf("runtime context value = %v, want value", got)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-manager.ctx.Done():
	default:
		t.Fatal("Manager.Close did not end the Gateway forward runtime")
	}
}
