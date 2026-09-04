package remote

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/middleware"
	"github.com/google/uuid"
)

func TestContractHTTPResponseAllowsAdditiveFieldsButRejectsMissingRequiredFields(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	var missing atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/version" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if missing.Load() {
			_, _ = writer.Write([]byte(`{"future":{"enabled":true}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"gitVersion":"v1.31.0","gatewayVersion":"v2-test","future":{"enabled":true}}`))
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	version, err := client.Version(context.Background(), serverProfile)
	if err != nil || version.GitVersion != "v1.31.0" {
		t.Fatalf("additive response = %#v, %v", version, err)
	}
	missing.Store(true)
	if _, err := client.Version(
		context.Background(),
		serverProfile,
	); err == nil ||
		!strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("missing required field = %v", err)
	}
}

//nolint:gocyclo // One integration-style test validates the complete Session HTTP lifecycle contract.
func TestSessionLifecycleUsesIdempotencyAndGenerationHeaders(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	sessionID := uuid.NewString()
	previousSessionID := uuid.NewString()
	relayPort := int32(41445)
	localPort := int32(8000)
	generation := uint64(1)
	state := remoteSessionActive
	spec, specHash := testNetworkSpec(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get(remoteParamNamespace) != "development" {
			t.Errorf("namespace = %q", request.URL.Query().Get(remoteParamNamespace))
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/sessions":
			if request.Header.Get(remoteHeaderIdempotencyKey) != "session-key" {
				t.Errorf("Idempotency-Key = %q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/heartbeat"):
			if request.Header.Get("If-Match") != `"1"` {
				t.Errorf("heartbeat If-Match = %q", request.Header.Get("If-Match"))
			}
			generation = 2
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/sync"):
			if request.Header.Get("If-Match") != "" {
				t.Errorf("sync If-Match = %q", request.Header.Get("If-Match"))
			}
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/traffic-bindings"):
			_ = json.NewEncoder(writer).Encode(map[string]any{"items": []TrafficBindingSession{{
				ID: uuid.NewString(), Name: "kubeloop-session", Namespace: "development",
				SessionID: previousSessionID, Mode: "PortForward", DesiredState: "Paused", Phase: "Paused",
				Target:      &TrafficBindingTarget{Kind: "Service", Name: "api"},
				DialAddress: "10.244.1.200:8443",
				Ports: []TrafficBindingPort{{
					TargetPort: 8443,
					RelayPort:  &relayPort,
					LocalHost:  "127.0.0.1",
					LocalPort:  &localPort,
					Protocol:   "TCP",
				}},
				CreatedAt: now,
			}}})
			return
		case request.Method == http.MethodDelete && strings.Contains(request.URL.Path, "/traffic-bindings/"):
			_ = json.NewEncoder(writer).Encode(map[string]bool{"deleted": true})
			return
		case request.Method == http.MethodDelete:
			if request.Header.Get("If-Match") != `"2"` {
				t.Errorf("disconnect If-Match = %q", request.Header.Get("If-Match"))
			}
			generation = 3
			state = "disconnected"
		}
		var capabilities *Capabilities
		if request.Method == http.MethodPost && request.URL.Path == "/api/sessions" {
			capabilities = &Capabilities{
				SchemaVersion: 1, IdentityID: "identity-1", Namespace: "development",
				GatewayVersion: "v2-test", Capabilities: []string{"cluster.tunnel", "pods.list"},
			}
		}
		_ = json.NewEncoder(writer).Encode(Session{
			ID: sessionID, Namespace: "development", State: state, Generation: generation,
			CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
			NetworkSpec: spec, NetworkSpecHash: specHash, Capabilities: capabilities,
		})
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	created, err := client.CreateSession(context.Background(), serverProfile, "development", "session-key")
	if err != nil || created.Generation != 1 || created.Capabilities == nil ||
		created.Capabilities.GatewayVersion != "v2-test" {
		t.Fatalf("create = %#v, %v", created, err)
	}
	cached, err := client.Capabilities(context.Background(), serverProfile, "development")
	if err != nil || len(cached.Capabilities) != 2 {
		t.Fatalf("Session capability cache = %#v, %v", cached, err)
	}
	synchronized, err := client.SyncTrafficBindings(context.Background(), serverProfile, created)
	if err != nil || synchronized.ID != created.ID || synchronized.Capabilities == nil {
		t.Fatalf("synchronized = %#v, %v", synchronized, err)
	}
	bindings, err := client.ListTrafficBindings(context.Background(), serverProfile, synchronized)
	if err != nil || len(bindings) != 1 || bindings[0].SessionID != previousSessionID ||
		bindings[0].Target == nil || bindings[0].Target.Name != "api" ||
		bindings[0].DialAddress != "10.244.1.200:8443" ||
		bindings[0].Ports[0].LocalHost != "127.0.0.1" ||
		bindings[0].Ports[0].LocalPort == nil || *bindings[0].Ports[0].LocalPort != localPort {
		t.Fatalf("TrafficBinding Sessions = %#v, %v", bindings, err)
	}
	if err := client.DeleteTrafficBinding(
		context.Background(), serverProfile, synchronized, bindings[0].ID,
	); err != nil {
		t.Fatalf("DeleteTrafficBinding() = %v", err)
	}
	heartbeat, err := client.HeartbeatSession(context.Background(), serverProfile, created)
	if err != nil || heartbeat.Generation != 2 || heartbeat.Capabilities == nil {
		t.Fatalf("heartbeat = %#v, %v", heartbeat, err)
	}
	disconnected, err := client.DisconnectSession(context.Background(), serverProfile, heartbeat)
	if err != nil || disconnected.Generation != 3 || disconnected.State != "disconnected" {
		t.Fatalf("disconnect = %#v, %v", disconnected, err)
	}
}

func TestIssueRelayTicketUsesAuthenticatedJSONRequest(t *testing.T) {
	const correlationID = "44444444-4444-4444-8444-444444444444"
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/sessions/"+session.ID+"/tickets" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.URL.Query().Get(remoteParamNamespace) != session.Namespace ||
			request.Header.Get("Content-Type") != "application/json" ||
			request.Header.Get("Authorization") != "Bearer access-token" ||
			request.Header.Get(middleware.Header) != correlationID {
			t.Errorf("query = %q, Content-Type = %q, Authorization = %q",
				request.URL.RawQuery, request.Header.Get("Content-Type"), request.Header.Get("Authorization"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		if string(body) != `{}` {
			t.Errorf("body = %s", body)
		}
		writer.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(writer).Encode(RelayTicket{
			TokenType: "KubeLoop-RelayTicket", Ticket: "signed.ticket.value", ExpiresAt: now.Add(45 * time.Second),
			DeviceID: "22222222-2222-4222-8222-222222222222",
			RelayID:  "relay-" + strings.Repeat("a", 64), Endpoint: "wss://relay.example/tunnel",
		})
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := client.IssueRelayTicket(
		middleware.WithID(context.Background(), correlationID),
		profile.Profile{ID: "service-1", BaseURL: server.URL}, session,
	)
	if err != nil || ticket.Ticket != "signed.ticket.value" || ticket.TokenType != "KubeLoop-RelayTicket" ||
		ticket.Endpoint != "wss://relay.example/tunnel" || ticket.DeviceID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("RelayTicket = %#v, %v", ticket, err)
	}
}

func TestRelayAssignmentAllowsWSAndWSS(t *testing.T) {
	relayID := "relay-" + strings.Repeat("a", 64)
	for _, endpoint := range []string{"ws://relay.example/tunnel", "wss://relay.example/tunnel"} {
		if !validRelayAssignment(relayID, endpoint) {
			t.Fatalf("validRelayAssignment(%q) = false", endpoint)
		}
	}
	if validRelayAssignment(relayID, "http://relay.example/tunnel") {
		t.Fatal("HTTP relay assignment was accepted")
	}
}
