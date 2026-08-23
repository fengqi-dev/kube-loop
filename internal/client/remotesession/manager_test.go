package remotesession

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type fakeGateway struct {
	mu             sync.Mutex
	createKeys     []string
	heartbeats     int
	disconnections int
	failCreateOnce bool
	tickets        int
	heartbeatErr   error
	disconnectErr  error
}

type blockingDisconnectGateway struct {
	*fakeGateway

	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (gateway *blockingDisconnectGateway) DisconnectSession(
	ctx context.Context,
	serverProfile profile.Profile,
	current remote.Session,
) (remote.Session, error) {
	gateway.once.Do(func() { close(gateway.started) })
	select {
	case <-gateway.release:
		return gateway.fakeGateway.DisconnectSession(ctx, serverProfile, current)
	case <-ctx.Done():
		return current, ctx.Err()
	}
}

func (gateway *fakeGateway) IssueRelayTicket(
	_ context.Context,
	_ profile.Profile,
	current remote.Session,
) (remote.RelayTicket, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	gateway.tickets++
	if current.State != remoteSessionActive {
		return remote.RelayTicket{}, errors.New("session is not active")
	}
	return remote.RelayTicket{
		TokenType: "KubeLoop-RelayTicket",
		Ticket:    "signed.ticket.value",
		ExpiresAt: time.Now().Add(time.Minute),
	}, nil
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
		ID: uuid.NewString(), Namespace: namespace, State: remoteSessionActive, Generation: 1,
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
	if gateway.heartbeatErr != nil {
		return current, gateway.heartbeatErr
	}
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
	if gateway.disconnectErr != nil {
		return current, gateway.disconnectErr
	}
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
	select {
	case update := <-manager.SessionUpdates():
		if update.ProfileID != serverProfile.ID || update.Session.Generation <= session.Generation {
			t.Fatalf("heartbeat Session update = %#v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not publish a Session update")
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

func TestCurrentDoesNotBlockDuringRemoteDisconnect(t *testing.T) {
	gateway := &blockingDisconnectGateway{
		fakeGateway: &fakeGateway{},
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-slow-disconnect"}
	session, err := manager.Connect(context.Background(), serverProfile, "development")
	if err != nil {
		t.Fatal(err)
	}
	disconnected := make(chan error, 1)
	go func() {
		disconnected <- manager.Disconnect(context.Background(), serverProfile.ID)
	}()
	select {
	case <-gateway.started:
	case <-time.After(time.Second):
		t.Fatal("remote disconnect did not start")
	}

	currentResult := make(chan struct {
		session remote.Session
		err     error
	}, 1)
	go func() {
		current, currentErr := manager.Current(serverProfile.ID)
		currentResult <- struct {
			session remote.Session
			err     error
		}{session: current, err: currentErr}
	}()
	select {
	case result := <-currentResult:
		if result.err != nil || result.session.ID != session.ID {
			t.Fatalf("Current during disconnect = %#v, %v", result.session, result.err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Current blocked on the remote disconnect")
	}
	connected := make(chan error, 1)
	go func() {
		_, connectErr := manager.Connect(
			context.Background(),
			profile.Profile{ID: "service-independent"},
			"development",
		)
		connected <- connectErr
	}()
	select {
	case err := <-connected:
		if err != nil {
			t.Fatalf("independent Connect failed: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("independent Connect blocked on another profile's remote disconnect")
	}

	close(gateway.release)
	if err := <-disconnected; err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Current(serverProfile.ID); err == nil {
		t.Fatal("completed disconnect retained the Session")
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
}

func TestConnectReplacesSessionRemovedByGateway(t *testing.T) {
	gateway := &fakeGateway{}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: "https://gateway.example.test"}
	initial, err := manager.Connect(context.Background(), serverProfile, "development")
	if err != nil {
		t.Fatal(err)
	}
	gateway.mu.Lock()
	gateway.heartbeatErr = &remote.APIError{Status: 404, Code: remote.CodeNotFound, Message: "resource not found"}
	gateway.mu.Unlock()
	if _, err := manager.Refresh(context.Background(), serverProfile.ID); err == nil {
		t.Fatal("expected the removed Session heartbeat to fail")
	}

	gateway.mu.Lock()
	gateway.heartbeatErr = nil
	gateway.mu.Unlock()
	replacement, err := manager.Connect(context.Background(), serverProfile, "development")
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID == initial.ID {
		t.Fatalf("reused removed Session %q", replacement.ID)
	}
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	if len(gateway.createKeys) != 2 {
		t.Fatalf("created Sessions = %d, want 2", len(gateway.createKeys))
	}
}

func TestShutdownReportsDisconnectFailureAndCanRetry(t *testing.T) {
	gateway := &fakeGateway{}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"service-a", "service-b"} {
		if _, err := manager.Connect(context.Background(), profile.Profile{ID: id}, "development"); err != nil {
			t.Fatal(err)
		}
	}
	disconnectFailure := errors.New("disconnect unavailable")
	gateway.mu.Lock()
	gateway.disconnectErr = disconnectFailure
	gateway.mu.Unlock()
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownContext); !errors.Is(err, disconnectFailure) {
		t.Fatalf("shutdown error = %v", err)
	}
	if _, err := manager.Current("service-a"); err != nil {
		t.Fatalf("failed shutdown forgot active Session: %v", err)
	}
	gateway.mu.Lock()
	gateway.disconnectErr = nil
	gateway.mu.Unlock()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Current("service-a"); err == nil {
		t.Fatal("successful shutdown retained Session")
	}
}

func TestNamespaceSwitchFailurePreservesCurrentSession(t *testing.T) {
	gateway := &fakeGateway{}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1"}
	current, err := manager.Connect(context.Background(), serverProfile, "development")
	if err != nil {
		t.Fatal(err)
	}
	disconnectFailure := errors.New("disconnect unavailable")
	gateway.mu.Lock()
	gateway.disconnectErr = disconnectFailure
	gateway.mu.Unlock()
	if _, err := manager.Connect(
		context.Background(),
		serverProfile,
		"production",
	); !errors.Is(
		err,
		disconnectFailure,
	) {
		t.Fatalf("namespace switch error = %v", err)
	}
	retained, err := manager.Current(serverProfile.ID)
	if err != nil || retained.ID != current.ID || retained.Namespace != "development" {
		t.Fatalf("retained Session = %#v, %v", retained, err)
	}
	gateway.mu.Lock()
	if len(gateway.createKeys) != 1 {
		gateway.mu.Unlock()
		t.Fatalf("sessions created after failed switch = %d", len(gateway.createKeys))
	}
	gateway.disconnectErr = nil
	gateway.mu.Unlock()
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
}

func TestDisconnectTreatsGoneSessionAsAlreadyClosed(t *testing.T) {
	gateway := &fakeGateway{}
	manager, err := New(gateway, Config{HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1"}
	if _, err := manager.Connect(context.Background(), serverProfile, "development"); err != nil {
		t.Fatal(err)
	}
	gateway.mu.Lock()
	gateway.disconnectErr = &remote.APIError{Status: 404, Code: remote.CodeNotFound}
	gateway.mu.Unlock()
	if err := manager.Disconnect(context.Background(), serverProfile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Current(serverProfile.ID); err == nil {
		t.Fatal("gone Session remained active")
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
}
