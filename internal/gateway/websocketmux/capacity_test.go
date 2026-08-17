package websocketmux

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/gateway/operations"
	"github.com/labstack/echo/v5"
)

type capacityGatewayState struct{}

func (capacityGatewayState) Ready() bool            { return true }
func (capacityGatewayState) Draining() bool         { return false }
func (capacityGatewayState) ActiveConnections() int { return 0 }

func TestCapacityLimitsSessionsAndStreamsWithoutBlockingHealth(t *testing.T) {
	identities := map[string]Identity{
		"one": {
			IdentityID: "identity-one", DeviceID: "11111111-1111-4111-8111-111111111111",
			SessionID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", SessionGeneration: 1, ExpiresAt: time.Now().Add(time.Minute),
		},
		"two": {
			IdentityID: "identity-two", DeviceID: "22222222-2222-4222-8222-222222222222",
			SessionID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", SessionGeneration: 1, ExpiresAt: time.Now().Add(time.Minute),
		},
		"three": {
			IdentityID: "identity-three", DeviceID: "33333333-3333-4333-8333-333333333333",
			SessionID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", SessionGeneration: 1, ExpiresAt: time.Now().Add(time.Minute),
		},
	}
	var activeStreams atomic.Int32
	var maximumStreams atomic.Int32
	releaseStreams := make(chan struct{})
	handler, err := NewHandler(ServerConfig{
		Authenticator: AuthenticatorFunc(func(request *http.Request) (Identity, error) {
			identity, ok := identities[strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")]
			if !ok {
				return Identity{}, fmt.Errorf("unknown test identity")
			}
			return identity, nil
		}),
		MaxSessions: 2, MaxSessionsPerUser: 1, MaxStreamsPerSession: 2,
		Handle: func(_ context.Context, _ Identity, connection net.Conn) {
			active := activeStreams.Add(1)
			for {
				current := maximumStreams.Load()
				if active <= current || maximumStreams.CompareAndSwap(current, active) {
					break
				}
			}
			defer activeStreams.Add(-1)
			<-releaseStreams
			_ = connection.Close()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := echo.New()
	router.Any("/tunnel", echo.WrapHandler(handler))
	operations.NewHandler(capacityGatewayState{}, handler).Register(router)
	server := httptest.NewServer(router)
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/tunnel"

	first := startCapacityClient(t, endpoint, "one", identities["one"].DeviceID)
	defer first.Close()
	duplicate, duplicateErr := Start(context.Background(), ClientConfig{
		URL: endpoint, Token: "one", DeviceID: identities["one"].DeviceID, PoolSize: 1, MaxPhysical: 1,
	})
	if duplicate != nil {
		_ = duplicate.Close()
	}
	if duplicateErr == nil {
		t.Fatalf("per-user capacity was not enforced: sessions=%d err=%v", handler.ActiveSessions(), duplicateErr)
	}
	waitCapacitySessions(t, handler, 1)
	second := startCapacityClient(t, endpoint, "two", identities["two"].DeviceID)
	defer second.Close()
	waitCapacitySessions(t, handler, 2)

	third, err := Start(context.Background(), ClientConfig{
		URL: endpoint, Token: "three", DeviceID: identities["three"].DeviceID, PoolSize: 1, MaxPhysical: 1,
	})
	if third != nil {
		_ = third.Close()
	}
	if err == nil || handler.ActiveSessions() != 2 {
		t.Fatalf("global capacity was not enforced: sessions=%d err=%v", handler.ActiveSessions(), err)
	}
	assertCapacityHealth(t, server.URL)

	connections := make([]net.Conn, 0, 3)
	for range 3 {
		connection, dialErr := net.DialTimeout("tcp", first.Address(), time.Second)
		if dialErr != nil {
			t.Fatalf("open logical stream: %v", dialErr)
		}
		connections = append(connections, connection)
	}
	t.Cleanup(func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for activeStreams.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := activeStreams.Load(); got != 2 {
		t.Fatalf("active logical streams = %d, want 2", got)
	}
	assertCapacityHealth(t, server.URL)
	close(releaseStreams)
	if maximumStreams.Load() != 2 {
		t.Fatalf("maximum concurrent logical streams = %d, want 2", maximumStreams.Load())
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close capacity session: %v", err)
	}
	waitCapacitySessions(t, handler, 1)
	replacement := startCapacityClient(t, endpoint, "three", identities["three"].DeviceID)
	defer replacement.Close()
	waitCapacitySessions(t, handler, 2)
	assertCapacityHealth(t, server.URL)
}

func startCapacityClient(t *testing.T, endpoint, token, deviceID string) *Forwarder {
	t.Helper()
	forwarder, err := Start(context.Background(), ClientConfig{
		URL: endpoint, Token: token, DeviceID: deviceID, PoolSize: 1, MaxPhysical: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return forwarder
}

func waitCapacitySessions(t *testing.T, handler *Handler, expected int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for handler.ActiveSessions() != expected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if handler.ActiveSessions() != expected {
		t.Fatalf("active sessions = %d, want %d", handler.ActiveSessions(), expected)
	}
}

func assertCapacityHealth(t *testing.T, baseURL string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{operations.LivePath, operations.ReadyPath, operations.MetricsPath} {
		response, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatalf("capacity health %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("capacity health %s status = %d", path, response.StatusCode)
		}
	}
}

func BenchmarkGatewayLogicalStreamRoundTrip(b *testing.B) {
	identity := Identity{
		IdentityID: "benchmark-identity", DeviceID: "44444444-4444-4444-8444-444444444444",
		SessionID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", SessionGeneration: 1,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	handler, err := NewHandler(ServerConfig{
		Authenticator: AuthenticatorFunc(func(*http.Request) (Identity, error) { return identity, nil }),
		MaxSessions:   4, MaxSessionsPerUser: 4, MaxStreamsPerSession: 128,
		Handle: func(_ context.Context, _ Identity, connection net.Conn) {
			defer connection.Close()
			_, _ = io.Copy(connection, connection)
		},
	})
	if err != nil {
		b.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	forwarder, err := Start(context.Background(), ClientConfig{
		URL: "ws" + strings.TrimPrefix(server.URL, "http"), Token: "benchmark",
		DeviceID: identity.DeviceID, PoolSize: 2, MaxPhysical: 2, MaxStreamsPerConn: 128,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer forwarder.Close()
	payload := make([]byte, 32<<10)
	response := make([]byte, len(payload))
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		connection, dialErr := net.DialTimeout("tcp", forwarder.Address(), time.Second)
		if dialErr != nil {
			b.Fatal(dialErr)
		}
		if _, writeErr := connection.Write(payload); writeErr != nil {
			_ = connection.Close()
			b.Fatal(writeErr)
		}
		if _, readErr := io.ReadFull(connection, response); readErr != nil {
			_ = connection.Close()
			b.Fatal(readErr)
		}
		_ = connection.Close()
	}
}
