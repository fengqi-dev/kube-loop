package remotesession

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func TestHeartbeatFailureBlocksTicketsUntilRefreshRecovers(t *testing.T) {
	gateway := &fakeGateway{}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: "https://gateway.example.test"}
	if _, err := manager.Connect(context.Background(), serverProfile, "development"); err != nil {
		t.Fatal(err)
	}
	heartbeatFailure := errors.New("heartbeat unavailable")
	gateway.mu.Lock()
	gateway.heartbeatErr = heartbeatFailure
	gateway.mu.Unlock()
	if _, err := manager.Refresh(context.Background(), serverProfile.ID); !errors.Is(err, heartbeatFailure) {
		t.Fatalf("refresh error = %v", err)
	}
	if _, err := manager.Current(serverProfile.ID); !errors.Is(err, heartbeatFailure) {
		t.Fatalf("current error = %v", err)
	}
	if _, err := manager.IssueRelayTicket(context.Background(), serverProfile.ID); !errors.Is(err, heartbeatFailure) {
		t.Fatalf("ticket error = %v", err)
	}
	gateway.mu.Lock()
	if gateway.tickets != 0 {
		gateway.mu.Unlock()
		t.Fatalf("tickets issued during heartbeat failure = %d", gateway.tickets)
	}
	gateway.heartbeatErr = nil
	gateway.mu.Unlock()
	if _, err := manager.Refresh(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.IssueRelayTicket(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
}
