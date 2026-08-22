package dataplane

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestBindConnectionContextInterruptsStartupIO(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = peer.Close()
	})
	ctx, cancel := context.WithCancel(t.Context())
	clearDeadline, err := bindConnectionContext(ctx, client)
	if err != nil {
		t.Fatal(err)
	}

	readResult := make(chan error, 1)
	go func() {
		var value [1]byte
		_, err := client.Read(value[:])
		readResult <- err
	}()
	cancel()
	if err := <-readResult; err == nil {
		t.Fatal("connection read was not interrupted by context cancellation")
	}
	clearDeadline()
}

func TestBindConnectionContextUsesContextDeadline(t *testing.T) {
	client, peer := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = peer.Close()
	})
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Minute))
	defer cancel()
	clearDeadline, err := bindConnectionContext(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	clearDeadline()
}

func TestContextConnectionError(t *testing.T) {
	original := errors.New("connection failed")
	if got := contextConnectionError(t.Context(), original); !errors.Is(got, original) {
		t.Fatalf("contextConnectionError() = %v, want original error", got)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := contextConnectionError(ctx, original); !errors.Is(got, context.Canceled) {
		t.Fatalf("contextConnectionError() = %v, want context cancellation", got)
	}
}
