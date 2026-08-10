package remotesession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/google/uuid"
)

type fakeGateway struct {
	mu             sync.Mutex
	createKeys     []string
	heartbeats     int
	disconnections int
	failCreateOnce bool
	tickets        int
}

func (gateway *fakeGateway) IssueRelayTicket(
	_ context.Context,
	_ profile.Profile,
	current remote.Session,
) (remote.RelayTicket, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.tickets++
	if current.State != "active" {
		return remote.RelayTicket{}, errors.New("session is not active")
	}
	return remote.RelayTicket{TokenType: "KubeLoop-RelayTicket", Ticket: "signed.ticket.value", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (gateway *fakeGateway) CreateSession(
	_ context.Context,
	_ profile.Profile,
	namespace, key string,
) (remote.Session, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.createKeys = append(gateway.createKeys, key)
	if gateway.failCreateOnce {
		gateway.failCreateOnce = false
		return remote.Session{}, errors.New("response lost")
	}
	now := time.Now().UTC()
	return remote.Session{
		ID: uuid.NewString(), Namespace: namespace, State: "active", Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}, nil
}

func (gateway *fakeGateway) HeartbeatSession(
	_ context.Context,
	_ profile.Profile,
	current remote.Session,
) (remote.Session, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.heartbeats++
	current.Generation++
	current.ExpiresAt = time.Now().Add(time.Minute)
	return current, nil
}

func (gateway *fakeGateway) DisconnectSession(
	_ context.Context,
	_ profile.Profile,
	current remote.Session,
) (remote.Session, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.disconnections++
	current.Generation++
	current.State = "disconnected"
	return current, nil
}

func TestManagerReusesPendingIdempotencyAndMaintainsHeartbeat(t *testing.T) {
	gateway := &fakeGateway{failCreateOnce: true}
	manager, err := New(gateway, Config{HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: "https://gateway.example.test"}
	if _, err := manager.Connect(context.Background(), serverProfile, "development"); err == nil {
		t.Fatal("expected first lost response")
	}
	session, err := manager.Connect(context.Background(), serverProfile, "development")
	if err != nil {
		t.Fatal(err)
	}
	gateway.mu.Lock()
	if len(gateway.createKeys) != 2 || gateway.createKeys[0] != gateway.createKeys[1] {
		t.Fatalf("idempotency keys = %v", gateway.createKeys)
	}
	gateway.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		current, currentErr := manager.Current(serverProfile.ID)
		if currentErr == nil && current.Generation > session.Generation {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat did not advance session: %#v, %v", current, currentErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	production, err := manager.Connect(context.Background(), serverProfile, "production")
	if err != nil || production.Namespace != "production" {
		t.Fatalf("namespace switch = %#v, %v", production, err)
	}
	refreshed, err := manager.Refresh(context.Background(), serverProfile.ID)
	if err != nil || refreshed.Generation <= production.Generation {
		t.Fatalf("immediate recovery heartbeat = %#v, %v", refreshed, err)
	}
	tokenSource := manager.RelayTicketSource(serverProfile.ID)
	ticket, err := tokenSource(context.Background())
	if err != nil || ticket.Ticket != "signed.ticket.value" {
		t.Fatalf("RelayTicket = %#v, %v", ticket, err)
	}
	gateway.mu.Lock()
	if gateway.disconnections != 1 {
		t.Fatalf("disconnects after namespace switch = %d", gateway.disconnections)
	}
	gateway.mu.Unlock()
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.disconnections != 2 {
		t.Fatalf("disconnects after shutdown = %d", gateway.disconnections)
	}
	if gateway.tickets != 1 {
		t.Fatalf("RelayTicket requests = %d", gateway.tickets)
	}
}

func TestManagerDisconnectClosesRemoteSessionAndForgetsIt(t *testing.T) {
	gateway := &fakeGateway{}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-logout", BaseURL: "https://gateway.example.test"}
	if _, err := manager.Connect(context.Background(), serverProfile, "development"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Disconnect(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Current(serverProfile.ID); err == nil {
		t.Fatal("disconnected remote Session remained active")
	}
	if err := manager.Disconnect(context.Background(), serverProfile.ID); err != nil {
		t.Fatalf("repeated disconnect = %v", err)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if gateway.disconnections != 1 {
		t.Fatalf("remote disconnects = %d", gateway.disconnections)
	}
}
