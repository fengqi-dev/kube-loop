package sessionregistry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDisconnectCancelsStreamsAndWaitsForRelease(t *testing.T) {
	registry := New(context.Background())
	firstContext, releaseFirst, err := registry.Attach(
		context.Background(),
		"session-a",
		"task-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	secondContext, releaseSecond, err := registry.Attach(
		context.Background(),
		"session-a",
		"task-b",
	)
	if err != nil {
		t.Fatal(err)
	}
	var orderMu sync.Mutex
	var order []string
	for name, pair := range map[string]struct {
		ctx     context.Context
		release func()
	}{"first": {firstContext, releaseFirst}, "second": {secondContext, releaseSecond}} {
		go func() {
			<-pair.ctx.Done()
			orderMu.Lock()
			order = append(order, name)
			orderMu.Unlock()
			pair.release()
		}()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := registry.Disconnect(ctx, "session-a"); err != nil {
		t.Fatal(err)
	}
	orderMu.Lock()
	if len(order) != 2 {
		t.Fatalf("release order = %v", order)
	}
	orderMu.Unlock()
	if err := registry.Disconnect(ctx, "session-a"); err != nil {
		t.Fatalf("idempotent disconnect: %v", err)
	}
}

func TestParentCancellationAndShutdownRejectNewStreams(t *testing.T) {
	registry := New(context.Background())
	parent, cancelParent := context.WithCancel(context.Background())
	streamContext, release, err := registry.Attach(
		parent,
		"session-a",
		"task-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	cancelParent()
	select {
	case <-streamContext.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not propagate")
	}
	release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := registry.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Attach(context.Background(), "session-b", "task-b"); !errors.Is(
		err,
		ErrClosed,
	) {
		t.Fatalf("Attach after shutdown = %v", err)
	}
}
