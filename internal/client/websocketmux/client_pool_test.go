package websocketmux

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	servermux "github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
)

func TestForwarderReplacesClosedPhysicalSession(t *testing.T) {
	const deviceID = "44444444-4444-4444-8444-444444444444"
	var physicalConnections atomic.Int32
	handler, err := servermux.NewHandler(servermux.ServerConfig{
		MaxSessions:        4,
		MaxSessionsPerUser: 4,
		Authenticator: servermux.AuthenticatorFunc(func(*http.Request) (servermux.Identity, error) {
			physicalConnections.Add(1)
			return servermux.Identity{
				IdentityID: "identity", DeviceID: deviceID, SessionID: uuid.NewString(),
				SessionGeneration: 1, ExpiresAt: time.Now().Add(time.Minute),
			}, nil
		}),
		Handle: func(_ context.Context, _ servermux.Identity, connection net.Conn) {
			_, _ = io.Copy(io.Discard, connection)
			_ = connection.Close()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	forwarder, err := Start(t.Context(), ClientConfig{
		URL: "ws" + server.URL[len("http"):], Token: "relay-ticket", DeviceID: deviceID,
		PoolSize: 1, MaxPhysical: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = forwarder.Close() })
	forwarder.mu.Lock()
	initialCount := len(forwarder.sessions)
	if initialCount != 1 {
		forwarder.mu.Unlock()
		t.Fatalf("initial sessions = %d, want 1", initialCount)
	}
	initial := forwarder.sessions[0]
	forwarder.mu.Unlock()
	if err := initial.session.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		forwarder.mu.Lock()
		remaining := len(forwarder.sessions)
		forwarder.mu.Unlock()
		if remaining == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stream, err := forwarder.OpenStream(t.Context())
	if err != nil {
		t.Fatalf("open stream after physical session failure: %v", err)
	}
	_ = stream.Close()
	if got := physicalConnections.Load(); got < 2 {
		t.Fatalf("physical connections after replacement = %d, want at least 2", got)
	}
}

func TestCommitSessionRejectsClosedForwarder(t *testing.T) {
	forwarder := &Forwarder{closed: true}
	if forwarder.commitSession(&pooledSession{}) {
		t.Fatal("closed Forwarder accepted a physical session")
	}
	if len(forwarder.sessions) != 0 {
		t.Fatal("closed Forwarder retained a physical session")
	}
}
