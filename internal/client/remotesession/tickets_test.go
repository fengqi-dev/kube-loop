package remotesession

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func TestRelayTicketSourceRejectsAChangedSessionGeneration(t *testing.T) {
	gateway := &fakeGateway{}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	serverProfile := profile.Profile{ID: "service-1", BaseURL: "https://gateway.example.test"}
	if _, err := manager.Connect(context.Background(), serverProfile, "development"); err != nil {
		t.Fatal(err)
	}
	boundSource := manager.RelayTicketSource(serverProfile.ID)
	if _, err := boundSource(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Refresh(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := boundSource(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "generation changed") {
		t.Fatalf("generation-bound RelayTicket source error = %v", err)
	}
	if _, err := manager.RelayTicketSource(serverProfile.ID)(context.Background()); err != nil {
		t.Fatalf("refreshed RelayTicket source = %v", err)
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.tickets != 2 {
		t.Fatalf("RelayTicket requests = %d, want 2", gateway.tickets)
	}
}
