package remote

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

type memoryStore struct {
	mu    sync.Mutex
	value credentials.Credential
}

func (store *memoryStore) Set(_ string, credential credentials.Credential) error {
	store.mu.Lock()
	store.value = credential
	store.mu.Unlock()
	return nil
}

func (store *memoryStore) Get(string) (credentials.Credential, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.value.AccessToken == "" {
		return credentials.Credential{}, credentials.ErrNotFound
	}
	return store.value, nil
}

func (store *memoryStore) Delete(string) error {
	store.mu.Lock()
	store.value = credentials.Credential{}
	store.mu.Unlock()
	return nil
}

type fakeRefresher struct {
	calls atomic.Int32
	now   time.Time
}

func (refresher *fakeRefresher) Refresh(
	_ context.Context,
	_ string,
	current credentials.Credential,
) (credentials.Credential, error) {
	refresher.calls.Add(1)
	current.AccessToken = "access-new"
	current.RefreshToken = "refresh-new"
	current.AccessExpiresAt = refresher.now.Add(time.Minute)
	current.RefreshExpiresAt = refresher.now.Add(time.Hour)
	return current, nil
}

func TestConcurrentExpiredRequestsRotateRefreshTokenOnce(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: credentials.Credential{
		AccessToken: "access-old", RefreshToken: "refresh-old", AccessExpiresAt: now.Add(-time.Second),
		RefreshExpiresAt: now.Add(time.Hour), DeviceID: "device-1",
	}}
	refresher := &fakeRefresher{now: now}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-new" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		_, _ = writer.Write([]byte(`{"items":[]}`))
	}))
	defer server.Close()
	client, err := New(store, refresher, Config{HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	start := make(chan struct{})
	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, requestErr := client.Namespaces(context.Background(), serverProfile)
			errorsChannel <- requestErr
		}()
	}
	close(start)
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	if refresher.calls.Load() != 1 {
		t.Fatalf("refresh calls = %d", refresher.calls.Load())
	}
}

func TestPodsNormalizesMissingCollections(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: validCredential(now)}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"items":[{"name":"api-0","namespace":"development","ready":true}]}`))
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
	pods, err := client.Pods(context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL}, "development")
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].Containers == nil || pods[0].Ports == nil {
		t.Fatalf("pods = %#v", pods)
	}
}

func TestUnauthorizedResponseRefreshesAndRetriesOnce(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: credentials.Credential{
		AccessToken: "access-old", RefreshToken: "refresh-old", AccessExpiresAt: now.Add(time.Minute),
		RefreshExpiresAt: now.Add(time.Hour), DeviceID: "device-1",
	}}
	refresher := &fakeRefresher{now: now}
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Header.Get("Authorization") == "Bearer access-old" {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"error":{"code":"UNAUTHENTICATED","message":"expired","requestId":"one"}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"gitVersion":"v1.31.0","gatewayVersion":"v2-test"}`))
	}))
	defer server.Close()
	client, err := New(store, refresher, Config{HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Version(context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if result.GitVersion != "v1.31.0" || calls.Load() != 2 || refresher.calls.Load() != 1 {
		t.Fatalf("result = %#v, HTTP calls = %d, refresh calls = %d", result, calls.Load(), refresher.calls.Load())
	}
}

func TestInventoryPaginationIsBoundedAndStable(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: validCredential(now)}
	refresher := &fakeRefresher{now: now}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("limit") != "500" {
			t.Errorf("limit = %q", request.URL.Query().Get("limit"))
		}
		if request.URL.Query().Get("continue") == "" {
			_, _ = writer.Write([]byte(`{"items":[{"name":"alpha","status":"Active"}],"continue":"page-2"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"items":[{"name":"beta","status":"Active"}]}`))
	}))
	defer server.Close()
	client, err := New(store, refresher, Config{HTTPClient: server.Client(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	items, err := client.Namespaces(context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Name != "alpha" || items[1].Name != "beta" {
		t.Fatalf("namespaces = %#v", items)
	}
}

func TestGatewayErrorsDoNotLeakRemoteDetailsOrTokens(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: validCredential(now)}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write(
			[]byte(
				`{"error":{"code":"FORBIDDEN","message":"operation is not permitted","requestId":"request-1"},"secret":"upstream detail"}`,
			),
		)
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
	_, err = client.Pods(context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL}, "development")
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Code != "FORBIDDEN" || apiError.RequestID != "request-1" {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "upstream detail") || strings.Contains(err.Error(), "access-token") {
		t.Fatalf("sensitive response leaked: %v", err)
	}
}

func TestCapabilitiesValidateNamespaceAndResponseBinding(t *testing.T) {
	now := time.Now()
	store := &memoryStore{value: validCredential(now)}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(Capabilities{
			SchemaVersion:  1,
			IdentityID:     "identity-1",
			Namespace:      "other",
			GatewayVersion: "v2-test",
			Capabilities:   []string{},
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
	if _, err := client.Capabilities(context.Background(), serverProfile, "Bad_Name"); err == nil {
		t.Fatal("invalid namespace was accepted")
	}
	if _, err := client.Capabilities(context.Background(), serverProfile, "development"); err == nil {
		t.Fatal("capabilities for a different namespace were accepted")
	}
}

func TestCapabilitiesCacheIsBoundedByIdentityNamespaceCredentialAndGatewayVersion(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	var capabilityCalls atomic.Int32
	gatewayVersion := "v2-a"
	identityID := "identity-a"
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/version":
			_ = json.NewEncoder(writer).Encode(Version{GitVersion: "v1.31.0", GatewayVersion: gatewayVersion})
		case "/api/capabilities":
			capabilityCalls.Add(1)
			_ = json.NewEncoder(writer).Encode(Capabilities{
				SchemaVersion: 1, IdentityID: identityID, Namespace: request.URL.Query().Get(remoteParamNamespace),
				GatewayVersion: gatewayVersion, Capabilities: []string{"pods.list"},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := New(store, &fakeRefresher{now: now}, Config{
		HTTPClient: server.Client(), Now: func() time.Time { return now }, CapabilityCacheTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	if _, err := client.Version(context.Background(), serverProfile); err != nil {
		t.Fatal(err)
	}
	first, err := client.Capabilities(context.Background(), serverProfile, "development")
	if err != nil {
		t.Fatal(err)
	}
	first.Capabilities[0] = "mutated"
	second, err := client.Capabilities(context.Background(), serverProfile, "development")
	if err != nil || capabilityCalls.Load() != 1 || second.Capabilities[0] != "pods.list" {
		t.Fatalf("cached result = %#v, calls = %d, error = %v", second, capabilityCalls.Load(), err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := client.Capabilities(
		context.Background(),
		serverProfile,
		"development",
	); err != nil ||
		capabilityCalls.Load() != 2 {
		t.Fatalf("expired cache calls = %d, error = %v", capabilityCalls.Load(), err)
	}
	gatewayVersion = "v2-b"
	if _, err := client.Version(context.Background(), serverProfile); err != nil {
		t.Fatal(err)
	}
	updated, err := client.Capabilities(context.Background(), serverProfile, "development")
	if err != nil || capabilityCalls.Load() != 3 || updated.GatewayVersion != "v2-b" {
		t.Fatalf("Gateway-version cache binding = %#v, calls = %d, error = %v", updated, capabilityCalls.Load(), err)
	}
	store.mu.Lock()
	store.value.RefreshToken = "refresh-other-identity"
	store.value.AccessToken = "access-other-identity"
	store.mu.Unlock()
	identityID = "identity-b"
	updated, err = client.Capabilities(context.Background(), serverProfile, "development")
	if err != nil || capabilityCalls.Load() != 4 || updated.IdentityID != "identity-b" {
		t.Fatalf("identity cache binding = %#v, calls = %d, error = %v", updated, capabilityCalls.Load(), err)
	}
	if _, err := client.Capabilities(
		context.Background(),
		serverProfile,
		"staging",
	); err != nil ||
		capabilityCalls.Load() != 5 {
		t.Fatalf("namespace cache binding calls = %d, error = %v", capabilityCalls.Load(), err)
	}
}

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

func TestSessionLifecycleUsesIdempotencyAndGenerationHeaders(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	sessionID := uuid.NewString()
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
			request.Header.Get("Authorization") != "Bearer access-token" {
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
		context.Background(), profile.Profile{ID: "service-1", BaseURL: server.URL}, session,
	)
	if err != nil || ticket.Ticket != "signed.ticket.value" || ticket.TokenType != "KubeLoop-RelayTicket" ||
		ticket.Endpoint != "wss://relay.example/tunnel" || ticket.DeviceID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("RelayTicket = %#v, %v", ticket, err)
	}
}

func TestPortForwardTaskLifecycleUsesSessionBoundGatewayAPI(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}
	taskID := uuid.NewString()
	state := remotetask.Running
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Query().Get(remoteParamNamespace) != session.Namespace ||
			!strings.HasPrefix(request.URL.Path, "/api/sessions/"+session.ID+"/port-forwards") {
			t.Errorf("request = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "pf-key" ||
				request.Header.Get("Content-Type") != "application/json" {
				t.Errorf("headers = %#v", request.Header)
			}
			var spec PortForwardSpec
			if err := json.NewDecoder(request.Body).
				Decode(&spec); err != nil || spec.Name != "api" ||
				spec.RemotePort != 8443 {
				t.Errorf("spec = %#v err = %v", spec, err)
			}
		}
		if request.Method == http.MethodDelete {
			state = remotetask.Stopped
		}
		document := PortForwardTask{
			ID: taskID, SessionID: session.ID, Namespace: session.Namespace, State: state,
			Kind: remoteResourceService, Name: "api", Protocol: remoteProtocolTCP, RemotePort: 8443,
			DialAddress: "10.96.0.20:8443", CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
		}
		if request.Method == http.MethodGet {
			_ = json.NewEncoder(writer).Encode(struct {
				Items []PortForwardTask `json:"items"`
			}{Items: []PortForwardTask{document}})
			return
		}
		_ = json.NewEncoder(writer).Encode(document)
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
	created, err := client.CreatePortForward(context.Background(), serverProfile, session, PortForwardSpec{
		Kind: remoteResourceService, Name: "api", Protocol: remoteProtocolTCP, RemotePort: 8443,
	}, "pf-key")
	if err != nil || created.DialAddress != "10.96.0.20:8443" {
		t.Fatalf("created = %#v err = %v", created, err)
	}
	listed, err := client.ListPortForwards(context.Background(), serverProfile, session)
	if err != nil || len(listed) != 1 || listed[0].ID != taskID {
		t.Fatalf("listed = %#v err = %v", listed, err)
	}
	stopped, err := client.StopPortForward(context.Background(), serverProfile, session, taskID)
	if err != nil || stopped.State != "stopped" || calls.Load() != 3 {
		t.Fatalf("stopped = %#v calls = %d err = %v", stopped, calls.Load(), err)
	}
}

func TestExchangeTaskControlLifecycle(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	taskID := uuid.NewString()
	state := remotetask.Pending
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" ||
			request.URL.Query().Get(remoteParamNamespace) != "development" {
			t.Errorf("headers=%#v query=%s", request.Header, request.URL.RawQuery)
		}
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "exchange-key" {
				t.Errorf("Idempotency-Key=%q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
			var spec ExchangeSpec
			if err := json.NewDecoder(request.Body).
				Decode(&spec); err != nil || spec.Service != "api" ||
				len(spec.Ports) != 2 {
				t.Errorf("Exchange spec=%#v err=%v", spec, err)
			}
		}
		if request.Method == http.MethodDelete {
			state = remotetask.Stopped
		}
		_ = json.NewEncoder(writer).Encode(ExchangeTask{
			ID:        taskID,
			SessionID: session.ID,
			Namespace: session.Namespace,
			State:     state,
			Service:   "api",
			ClusterIP: "10.96.0.20",
			Ports: []ExchangePort{
				{Name: "dns", ServicePort: 53, Protocol: remoteProtocolUDP},
				{Name: "http", ServicePort: 80, Protocol: remoteProtocolTCP},
			},
			CreatedAt: now,
			UpdatedAt: now,
			ExpiresAt: session.ExpiresAt,
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
	created, err := client.CreateExchange(context.Background(), serverProfile, session, ExchangeSpec{
		Service: "api",
		Ports: []ExchangePort{
			{ServicePort: 53, Protocol: remoteProtocolUDP},
			{ServicePort: 80, Protocol: remoteProtocolTCP},
		},
	}, "exchange-key")
	if err != nil || created.ID != taskID {
		t.Fatalf("created Exchange=%#v err=%v", created, err)
	}
	loaded, err := client.GetExchange(context.Background(), serverProfile, session, taskID)
	if err != nil || loaded.ClusterIP != "10.96.0.20" {
		t.Fatalf("loaded Exchange=%#v err=%v", loaded, err)
	}
	stopped, err := client.StopExchange(context.Background(), serverProfile, session, taskID)
	if err != nil || stopped.State != "stopped" {
		t.Fatalf("stopped Exchange=%#v err=%v", stopped, err)
	}
}

func TestMirrorTaskControlLifecycle(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	taskID := uuid.NewString()
	state := remotetask.Pending
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" ||
			request.URL.Query().Get(remoteParamNamespace) != "development" {
			t.Errorf("headers=%#v query=%s", request.Header, request.URL.RawQuery)
		}
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "mirror-key" {
				t.Errorf("Idempotency-Key=%q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
			var spec MirrorSpec
			if err := json.NewDecoder(request.Body).
				Decode(&spec); err != nil || spec.Service != "api" ||
				len(spec.Ports) != 2 {
				t.Errorf("Mirror spec=%#v err=%v", spec, err)
			}
		}
		if request.Method == http.MethodDelete {
			state = remotetask.Stopped
		}
		_ = json.NewEncoder(writer).Encode(MirrorTask{
			ID:        taskID,
			SessionID: session.ID,
			Namespace: session.Namespace,
			State:     state,
			Service:   "api",
			ClusterIP: "10.96.0.20",
			Ports: []MirrorPort{
				{Name: "dns", ServicePort: 53, Protocol: remoteProtocolUDP},
				{Name: "http", ServicePort: 80, Protocol: remoteProtocolTCP},
			},
			CreatedAt: now,
			UpdatedAt: now,
			ExpiresAt: session.ExpiresAt,
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
	created, err := client.CreateMirror(context.Background(), serverProfile, session, MirrorSpec{
		Service: "api",
		Ports: []MirrorPort{
			{ServicePort: 53, Protocol: remoteProtocolUDP},
			{ServicePort: 80, Protocol: remoteProtocolTCP},
		},
	}, "mirror-key")
	if err != nil || created.ID != taskID {
		t.Fatalf("created Mirror=%#v err=%v", created, err)
	}
	loaded, err := client.GetMirror(context.Background(), serverProfile, session, taskID)
	if err != nil || loaded.ClusterIP != "10.96.0.20" {
		t.Fatalf("loaded Mirror=%#v err=%v", loaded, err)
	}
	stopped, err := client.StopMirror(context.Background(), serverProfile, session, taskID)
	if err != nil || stopped.State != "stopped" {
		t.Fatalf("stopped Mirror=%#v err=%v", stopped, err)
	}
}

func TestPreviewTaskControlLifecycle(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	taskID := uuid.NewString()
	var state atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" ||
			request.URL.Query().Get(remoteParamNamespace) != "development" {
			t.Errorf("headers=%#v query=%s", request.Header, request.URL.RawQuery)
		}
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "preview-key" {
				t.Errorf("Idempotency-Key=%q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
			var spec PreviewSpec
			if err := json.NewDecoder(request.Body).
				Decode(&spec); err != nil || spec.Name != "local-api" ||
				len(spec.Ports) != 2 {
				t.Errorf("Preview spec=%#v err=%v", spec, err)
			}
		}
		if request.Method == http.MethodDelete {
			state.Store(2)
		}
		if request.Method == http.MethodGet && state.Load() == 0 {
			state.Store(1)
		}
		taskState := remotetask.Pending
		clusterIP := ""
		switch state.Load() {
		case 1:
			taskState, clusterIP = remotetask.Running, "10.96.0.42"
		case 2:
			taskState, clusterIP = remotetask.Stopped, "10.96.0.42"
		}
		_ = json.NewEncoder(writer).Encode(PreviewTask{
			ID:        taskID,
			SessionID: session.ID,
			Namespace: session.Namespace,
			State:     taskState,
			Name:      "local-api",
			ClusterIP: clusterIP,
			Ports: []PreviewPort{
				{Name: "dns", ServicePort: 53, Protocol: remoteProtocolUDP},
				{Name: "http", ServicePort: 80, Protocol: remoteProtocolTCP},
			},
			CreatedAt: now,
			UpdatedAt: now,
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
	created, err := client.CreatePreview(context.Background(), serverProfile, session, PreviewSpec{
		Name: "local-api",
		Ports: []PreviewPort{
			{ServicePort: 53, Protocol: remoteProtocolUDP},
			{ServicePort: 80, Protocol: remoteProtocolTCP},
		},
	}, "preview-key")
	if err != nil || created.ID != taskID || created.ClusterIP != "" {
		t.Fatalf("created Preview=%#v err=%v", created, err)
	}
	loaded, err := client.GetPreview(context.Background(), serverProfile, session, taskID)
	if err != nil || loaded.State != "running" || loaded.ClusterIP != "10.96.0.42" {
		t.Fatalf("loaded Preview=%#v err=%v", loaded, err)
	}
	stopped, err := client.StopPreview(context.Background(), serverProfile, session, taskID)
	if err != nil || stopped.State != "stopped" {
		t.Fatalf("stopped Preview=%#v err=%v", stopped, err)
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

func TestPreviewTaskRequiresClusterIPOnlyAfterItIsRunning(t *testing.T) {
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), Namespace: "development"}
	task := PreviewTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: remoteTaskPending,
		Name: "local-api", Ports: []PreviewPort{{ServicePort: 80, Protocol: remoteProtocolTCP}},
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := validatePreviewTask(task, session); err != nil {
		t.Fatalf("pending Preview without ClusterIP was rejected: %v", err)
	}
	task.State = "running"
	if _, err := validatePreviewTask(task, session); err == nil {
		t.Fatal("running Preview without ClusterIP was accepted")
	}
	task.ClusterIP = "10.96.0.42"
	if _, err := validatePreviewTask(task, session); err != nil {
		t.Fatalf("running Preview with ClusterIP was rejected: %v", err)
	}
}

func TestPodExecTaskOpensAuthenticatedWebSocketStream(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID:         uuid.NewString(),
		Namespace:  "development",
		State:      remoteSessionActive,
		Generation: 1,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(time.Minute),
	}
	task := ExecTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: remoteTaskPending,
		Pod: "api-0", Container: "api", CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "exec-key" ||
				request.Header.Get("Authorization") != "Bearer access-token" {
				t.Errorf("headers = %#v", request.Header)
			}
			_ = json.NewEncoder(writer).Encode(task)
			return
		}
		if request.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = connection.CloseNow() }()
		stdout, _ := execstream.Encode(execstream.Frame{Type: execstream.Stdout, Payload: []byte("hello")})
		exit, _ := execstream.EncodeExit(execstream.ExitStatus{})
		_ = connection.Write(request.Context(), websocket.MessageBinary, stdout)
		_ = connection.Write(request.Context(), websocket.MessageBinary, exit)
		_ = connection.Close(websocket.StatusNormalClosure, "done")
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
	created, err := client.CreateExecTask(context.Background(), serverProfile, session, ExecSpec{
		Pod: "api-0", Container: "api", Command: []string{"/bin/sh"},
	}, "exec-key")
	if err != nil || created.ID != task.ID {
		t.Fatalf("created = %#v err = %v", created, err)
	}
	connection, err := client.OpenExecStream(context.Background(), serverProfile, session, created)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, connection.CloseNow)
	_, encoded, err := connection.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := execstream.Decode(encoded)
	if err != nil || frame.Type != execstream.Stdout || string(frame.Payload) != "hello" {
		t.Fatalf("frame = %#v err = %v", frame, err)
	}
}

func TestFileTransferTaskUsesAuthenticatedControlAndWebSocketAPIs(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	task := FileTransferTask{
		ID:         uuid.NewString(),
		SessionID:  session.ID,
		Namespace:  session.Namespace,
		State:      remoteTaskPending,
		Direction:  remoteDirectionDownload,
		Kind:       remoteKindFile,
		Pod:        "api-0",
		Container:  "api",
		RemotePath: "/workspace/data.bin",
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  session.ExpiresAt,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if strings.HasSuffix(request.URL.Path, "/stream") {
			connection, err := websocket.Accept(writer, request, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = connection.CloseNow() }()
			progress, _ := filestream.EncodeProgress(filestream.ProgressStatus{Total: 4})
			result, _ := filestream.EncodeResult(
				filestream.TransferResult{Status: filestream.ResultSucceeded, Transferred: 4},
			)
			_ = connection.Write(request.Context(), websocket.MessageBinary, progress)
			_ = connection.Write(request.Context(), websocket.MessageBinary, result)
			return
		}
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "file-key" {
				t.Errorf("Idempotency-Key = %q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
			var spec FileTransferSpec
			if err := json.NewDecoder(request.Body).
				Decode(&spec); err != nil || spec.RemotePath != task.RemotePath ||
				spec.Direction != remoteDirectionDownload {
				t.Errorf("spec = %#v err = %v", spec, err)
			}
		}
		_ = json.NewEncoder(writer).Encode(task)
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
	created, err := client.CreateFileTransferTask(context.Background(), serverProfile, session, FileTransferSpec{
		Direction: remoteDirectionDownload, Kind: remoteKindFile, Pod: "api-0", RemotePath: task.RemotePath,
	}, "file-key")
	if err != nil || created.ID != task.ID {
		t.Fatalf("created = %#v err = %v", created, err)
	}
	loaded, err := client.GetFileTransferTask(context.Background(), serverProfile, session, task.ID)
	if err != nil || loaded.ID != task.ID {
		t.Fatalf("loaded = %#v err = %v", loaded, err)
	}
	connection, err := client.OpenFileTransferStream(context.Background(), serverProfile, session, created)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, connection.CloseNow)
	_, encoded, err := connection.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	frame, err := filestream.Decode(encoded)
	if err != nil || frame.Type != filestream.Progress {
		t.Fatalf("frame = %#v err = %v", frame, err)
	}
}

func TestFileTransferClientRejectsUnsafeLocalSpecAndGatewayTask(t *testing.T) {
	for _, spec := range []FileTransferSpec{
		{Direction: remoteDirectionUpload, Kind: remoteKindFile, Pod: "api-0", RemotePath: "../escape", Size: 1, Checksum: strings.Repeat("00", 32)},
		{Direction: remoteDirectionUpload, Kind: remoteKindDirectory, Pod: "api-0", RemotePath: "/workspace/data", Size: 1, Offset: 1, Checksum: strings.Repeat("00", 32)},
		{Direction: remoteDirectionDownload, Kind: remoteKindFile, Pod: "api-0", RemotePath: "/workspace/data", Size: 1},
	} {
		if err := validateFileTransferSpec(&spec); err == nil {
			t.Fatalf("unsafe spec was accepted: %#v", spec)
		}
	}
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), Namespace: "development"}
	task := FileTransferTask{
		ID:         uuid.NewString(),
		SessionID:  session.ID,
		Namespace:  session.Namespace,
		State:      remoteTaskPending,
		Direction:  remoteDirectionDownload,
		Kind:       remoteKindFile,
		Pod:        "api-0",
		Container:  "api",
		RemotePath: "/workspace/../escape",
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(time.Minute),
	}
	if _, err := validateFileTransferTask(task, session); err == nil {
		t.Fatal("unsafe Gateway task was accepted")
	}
}

func TestPodFileClientUsesSessionBoundListAndIdempotentMutationAPIs(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	taskID := uuid.NewString()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" ||
			request.URL.Query().Get(remoteParamNamespace) != session.Namespace {
			t.Errorf("request headers/query = %#v %s", request.Header, request.URL.RawQuery)
		}
		switch {
		case strings.HasSuffix(request.URL.Path, "/list"):
			var spec PodFileSpec
			if err := json.NewDecoder(request.Body).Decode(&spec); err != nil || spec.Path != "/workspace" {
				t.Errorf("list spec = %#v err = %v", spec, err)
			}
			_ = json.NewEncoder(writer).Encode(PodFileList{
				SessionID: session.ID,
				Namespace: session.Namespace,
				Pod:       "api-0",
				Container: "api",
				Path:      "/workspace",
				Items: []PodFileEntry{
					{Name: "logs", Path: "/workspace/logs", Kind: remoteKindDirectory, Mode: "0755", ModifiedAt: now},
				},
			})
		case strings.HasSuffix(request.URL.Path, "/create"):
			if request.Header.Get(remoteHeaderIdempotencyKey) != "pod-file-key" {
				t.Errorf("Idempotency-Key = %q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
			_ = json.NewEncoder(writer).Encode(PodFileTask{
				ID:        taskID,
				SessionID: session.ID,
				Namespace: session.Namespace,
				State:     "stopped",
				Action:    remoteActionCreate,
				Pod:       "api-0",
				Container: "api",
				Path:      "/workspace/new",
				Kind:      remoteKindDirectory,
				Result:    PodFileResult{Completed: true},
				CreatedAt: now,
				UpdatedAt: now,
				ExpiresAt: session.ExpiresAt,
			})
		case strings.Contains(request.URL.Path, "/operations/"):
			_ = json.NewEncoder(writer).Encode(PodFileTask{
				ID:        taskID,
				SessionID: session.ID,
				Namespace: session.Namespace,
				State:     "stopped",
				Action:    remoteActionCreate,
				Pod:       "api-0",
				Container: "api",
				Path:      "/workspace/new",
				Kind:      remoteKindDirectory,
				Result:    PodFileResult{Completed: true},
				CreatedAt: now,
				UpdatedAt: now,
				ExpiresAt: session.ExpiresAt,
			})
		default:
			http.NotFound(writer, request)
		}
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
	listing, err := client.ListPodFiles(
		context.Background(),
		serverProfile,
		session,
		PodFileSpec{Pod: "api-0", Path: "/workspace"},
	)
	if err != nil || len(listing.Items) != 1 || listing.Items[0].Name != "logs" {
		t.Fatalf("listing = %#v err = %v", listing, err)
	}
	created, err := client.CreatePodFileOperation(
		context.Background(),
		serverProfile,
		session,
		remoteActionCreate,
		PodFileSpec{
			Pod: "api-0", Path: "/workspace/new", Kind: remoteKindDirectory,
		},
		"pod-file-key",
	)
	if err != nil || created.ID != taskID || !created.Result.Completed {
		t.Fatalf("created = %#v err = %v", created, err)
	}
	loaded, err := client.GetPodFileOperation(context.Background(), serverProfile, session, taskID)
	if err != nil || loaded.ID != taskID {
		t.Fatalf("loaded = %#v err = %v", loaded, err)
	}
}

func TestPodFileClientRejectsUnsafeSpecsAndUnboundResponses(t *testing.T) {
	for action, spec := range map[string]PodFileSpec{
		remoteActionList:   {Pod: "api-0", Path: "/workspace/../etc"},
		remoteActionCreate: {Pod: "api-0", Path: "/", Kind: remoteKindFile},
		"rename":           {Pod: "api-0", Path: "/workspace/a", Destination: "/"},
		remoteActionDelete: {Pod: "api-0", Path: "relative"},
	} {
		if err := validatePodFileSpec(action, &spec); err == nil {
			t.Fatalf("unsafe %s spec accepted: %#v", action, spec)
		}
	}
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), Namespace: "development"}
	_, err := validatePodFileList(PodFileList{
		SessionID: uuid.NewString(),
		Namespace: session.Namespace,
		Pod:       "api-0",
		Container: "api",
		Path:      "/workspace",
		Items:     []PodFileEntry{},
	}, session, PodFileSpec{Pod: "api-0", Path: "/workspace"})
	if err == nil {
		t.Fatal("listing bound to another Session was accepted")
	}
	_, err = validatePodFileTask(PodFileTask{
		ID:        uuid.NewString(),
		SessionID: session.ID,
		Namespace: session.Namespace,
		State:     "failed",
		Action:    remoteActionDelete,
		Pod:       "api-0",
		Container: "api",
		Path:      "/workspace/a",
		Result:    PodFileResult{},
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(time.Minute),
	}, session)
	if err == nil {
		t.Fatal("failed Task without a bounded error was accepted")
	}
}

func testNetworkSpec(t *testing.T) (networkspec.Spec, string) {
	t.Helper()
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	return spec, hash
}

func validCredential(now time.Time) credentials.Credential {
	return credentials.Credential{
		AccessToken: "access-token", RefreshToken: "refresh-token", AccessExpiresAt: now.Add(time.Minute),
		RefreshExpiresAt: now.Add(time.Hour), DeviceID: "device-1",
	}
}
