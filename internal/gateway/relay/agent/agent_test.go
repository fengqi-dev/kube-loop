package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
)

type staticRegistryAuthenticator struct{ identity relaycontrol.PeerIdentity }

func (authenticator staticRegistryAuthenticator) Authenticate(*http.Request) (relaycontrol.PeerIdentity, error) {
	return authenticator.identity, nil
}

type testRuntimeReporter struct {
	mu       sync.Mutex
	draining bool
}

func (reporter *testRuntimeReporter) Snapshot() (relaycontrol.State, relaycontrol.Capacity) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	state := relaycontrol.StateReady
	if reporter.draining {
		state = relaycontrol.StateDraining
	}
	return state, relaycontrol.Capacity{
		MaximumPhysicalConnections: 16, MaximumLogicalStreams: 1024,
	}
}

func (reporter *testRuntimeReporter) BeginDrain() {
	reporter.mu.Lock()
	reporter.draining = true
	reporter.mu.Unlock()
}

type testApplier struct {
	mu            sync.Mutex
	keyGeneration uint64
}

func (applier *testApplier) Apply(
	_, _ string,
	keys relaycontrol.VerificationKeySet,
) error {
	applier.mu.Lock()
	applier.keyGeneration = keys.Generation
	applier.mu.Unlock()
	return nil
}

func (applier *testApplier) AppliedKeyGeneration() uint64 {
	applier.mu.Lock()
	defer applier.mu.Unlock()
	return applier.keyGeneration
}

func TestAgentAllowsWSAndWSSAdvertisedEndpoints(t *testing.T) {
	for _, endpoint := range []string{"ws://relay.example/tunnel", "wss://relay.example/tunnel"} {
		_, err := New(Config{
			ControlPlaneURL: "https://control-plane.example",
			Endpoint:        endpoint, HTTPClient: http.DefaultClient,
			Reporter: &testRuntimeReporter{}, Applier: &testApplier{},
		})
		if err != nil {
			t.Fatalf("New endpoint %q: %v", endpoint, err)
		}
	}
}

func TestAgentRegistersAppliesControlStateAndAcknowledgesHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := relaycontrol.NewVerificationKeySet(
		1,
		map[string]ed25519.PublicKey{"primary": publicKey},
		now.Add(-time.Minute),
		now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := relayregistry.New(relayregistry.Config{
		TicketIssuer: "https://control-plane.example.test", VerificationKeys: keys,
		HeartbeatAfter: time.Second, LeaseDuration: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := relaycontrol.PeerIdentity{
		TrustDomain: "cluster.local", Namespace: "kubeloop", ServiceAccount: "gateway", PodUID: "pod-uid",
	}
	handler, err := relayregistry.NewHTTPHandler(registry, staticRegistryAuthenticator{identity: identity}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer projected-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if requests.Add(1) <= 2 {
			http.Error(writer, "Control Plane is starting", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("projected-token\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	reporter := &testRuntimeReporter{}
	applier := &testApplier{}
	agent, err := New(Config{
		ControlPlaneURL: server.URL, Endpoint: "wss://relay.example/tunnel",
		HTTPClient: server.Client(), BearerTokenFile: tokenFile, Reporter: reporter, Applier: applier,
		RegistrationAttempts: 3, RegistrationRetryDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if err := agent.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !agent.Ready() {
		t.Fatal("Agent is not ready after registration acknowledgement")
	}
	statuses := registry.Snapshot()
	if len(statuses) != 1 || statuses[0].AppliedKeyGeneration != 1 || statuses[0].RelayID != agent.RelayID() {
		t.Fatalf("Registry status = %#v", statuses)
	}
	if requests.Load() != 4 {
		t.Fatalf(
			"Control Plane requests = %d, want two failed registrations, one registration, and one heartbeat",
			requests.Load(),
		)
	}
	agent.Stop()
	select {
	case <-agent.Done():
	case <-time.After(time.Second):
		t.Fatal("Agent did not stop")
	}
	if agent.Ready() {
		t.Fatal("stopped Agent remained ready")
	}
}

func TestAgentStartDoesNotRetryPermanentRegistrationFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	agent, err := New(Config{
		ControlPlaneURL: server.URL, Endpoint: "wss://relay.example/tunnel",
		HTTPClient: server.Client(), Reporter: &testRuntimeReporter{}, Applier: &testApplier{},
		RegistrationAttempts: 3, RegistrationRetryDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.Start(t.Context()); err == nil {
		t.Fatal("Agent start accepted a permanent registration failure")
	}
	if requests.Load() != 1 {
		t.Fatalf("Control Plane requests = %d, want 1", requests.Load())
	}
}

func TestAgentSerializesConcurrentStartAndAllowsRetryAfterFailure(t *testing.T) {
	firstRequest := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			close(firstRequest)
			<-releaseFirst
		}
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	defer release()

	agent, err := New(Config{
		ControlPlaneURL: server.URL, Endpoint: "wss://relay.example/tunnel",
		HTTPClient: server.Client(), Reporter: &testRuntimeReporter{}, Applier: &testApplier{},
		RegistrationAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstResult := make(chan error, 1)
	go func() { firstResult <- agent.Start(t.Context()) }()
	select {
	case <-firstRequest:
	case <-time.After(time.Second):
		t.Fatal("first registration did not start")
	}
	if err := agent.Start(t.Context()); err == nil {
		t.Fatal("concurrent Agent start was accepted")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("concurrent registration requests = %d, want 1", got)
	}
	release()
	if err := <-firstResult; err == nil {
		t.Fatal("first Agent start accepted a failed registration")
	}
	if err := agent.Start(t.Context()); err == nil {
		t.Fatal("Agent retry accepted a failed registration")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("registration requests after retry = %d, want 2", got)
	}
}

func TestAgentStopCancelsRegistrationInProgress(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRequest) }) }
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		select {
		case <-request.Context().Done():
		case <-releaseRequest:
		}
	}))
	defer server.Close()
	defer release()

	agent, err := New(Config{
		ControlPlaneURL: server.URL, Endpoint: "wss://relay.example/tunnel",
		HTTPClient: server.Client(), Reporter: &testRuntimeReporter{}, Applier: &testApplier{},
		RegistrationAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- agent.Start(ctx) }()
	select {
	case <-requestStarted:
	case <-ctx.Done():
		t.Fatal("registration did not start")
	}
	agent.Stop()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Start error = %v, want context canceled", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Stop did not cancel registration")
	}
}

func TestRetryableRegistrationErrorIncludesConnectionFailure(t *testing.T) {
	err := fmt.Errorf("register relay: %w", &url.Error{
		Op:  "Post",
		URL: "https://control-plane.invalid/relay/v1/relays/register",
		Err: errors.New("connection refused"),
	})
	if !retryableRegistrationError(err) {
		t.Fatal("expected a wrapped connection failure to be retryable")
	}
}

func TestAgentReadinessRequiresCurrentLeaseAndHealthyHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	agent := &Agent{
		config:         Config{Now: func() time.Time { return now }},
		lifecycle:      lifecycleRunning,
		relayID:        "relay-1",
		leaseExpiresAt: now.Add(time.Minute),
	}
	if !agent.Ready() {
		t.Fatal("current acknowledged Relay lease was not ready")
	}

	now = now.Add(2 * time.Minute)
	if agent.Ready() {
		t.Fatal("expired Relay lease remained ready")
	}

	agent.leaseExpiresAt = now.Add(time.Minute)
	agent.setError(errors.New("heartbeat failed"))
	if agent.Ready() {
		t.Fatal("failed Relay heartbeat remained ready")
	}

	agent.lastError = nil
	agent.relayID = ""
	if agent.Ready() {
		t.Fatal("unregistered Relay reported ready")
	}
}

func TestAgentDoJSONUsesTrustedTransportAndTypedErrors(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("projected-token\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer projected-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/internal/v1/test":
			if request.Method != http.MethodPost {
				t.Errorf("method = %q", request.Method)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"ok":true}`))
		case "/internal/v1/error":
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"error":{"code":"CONFLICT"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	agent, err := New(Config{
		ControlPlaneURL: server.URL, Endpoint: "wss://relay.example/tunnel",
		HTTPClient: server.Client(), BearerTokenFile: tokenFile,
		Reporter: &testRuntimeReporter{}, Applier: &testApplier{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		OK bool `json:"ok"`
	}
	if err := agent.DoJSON(
		t.Context(),
		http.MethodPost,
		"/internal/v1/test",
		map[string]string{"request": "value"},
		&output,
	); err != nil || !output.OK {
		t.Fatalf("DoJSON output = %#v err = %v", output, err)
	}
	if err := agent.DoJSON(t.Context(), http.MethodGet, "/public", nil, nil); err == nil {
		t.Fatal("non-internal path was accepted")
	}
	err = agent.DoJSON(t.Context(), http.MethodPost, "/internal/v1/error", struct{}{}, nil)
	var httpError *HTTPError
	if !errors.As(err, &httpError) || httpError.Status != http.StatusConflict ||
		httpError.Code != "CONFLICT" || httpError.HTTPStatus() != http.StatusConflict ||
		httpError.Error() != "Relay control HTTP 409 (CONFLICT)" || !isLeaseError(err) {
		t.Fatalf("typed HTTP error = %#v err = %v", httpError, err)
	}
	if isLeaseError(&HTTPError{Status: http.StatusUnauthorized}) || isLeaseError(errors.New("conflict")) {
		t.Fatal("non-lease error was classified as a lease failure")
	}
}

func TestAgentDrainMarksRuntimeBeforeLeaseHeartbeat(t *testing.T) {
	reporter := &testRuntimeReporter{}
	agent := &Agent{config: Config{Reporter: reporter}}
	if err := agent.Drain(t.Context()); err == nil {
		t.Fatal("drain without a Relay lease succeeded")
	}
	state, _ := reporter.Snapshot()
	if state != relaycontrol.StateDraining {
		t.Fatalf("runtime state after drain = %q", state)
	}
}

func TestWaitForRetryStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForRetry(ctx, time.Hour) {
		t.Fatal("retry wait completed after context cancellation")
	}
}

func TestTicketAuthenticatorAppliesAudienceAndKeysAtomically(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := relaycontrol.NewVerificationKeySet(
		3,
		map[string]ed25519.PublicKey{"primary": publicKey},
		now.Add(-time.Minute),
		now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	authenticator, err := NewTicketAuthenticator(TicketAuthenticatorConfig{
		RequiredOperation: "tunnel", ReplayEntries: 10,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	relayID := "relay-" + strings.Repeat("a", 64)
	if err := authenticator.Apply("https://controlPlane.example", relayID, keys); err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Apply("https://replacement.example", relayID, keys); err == nil {
		t.Fatal("RelayTicket issuer change was accepted")
	}
	signer, err := relayticket.NewSigner("primary", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := signer.Sign(relayticket.Claims{
		Version:           relayticket.Version,
		Issuer:            "https://controlPlane.example",
		Audience:          relayID,
		IdentityID:        uuid.NewString(),
		DeviceID:          uuid.NewString(),
		SessionID:         sessionID,
		SessionGeneration: 1,
		Namespace:         "development",
		Operations:        []string{"tunnel"},
		NetworkSpecHash:   strings.Repeat("b", 64),
		TicketID:          uuid.NewString(),
		IssuedAt:          now.Unix(),
		NotBefore:         now.Unix(),
		ExpiresAt:         now.Add(30 * time.Second).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://relay.example/tunnel", nil)
	request.Header.Set("Authorization", "Bearer "+ticket)
	if _, err := authenticator.Verify(request); err != nil {
		t.Fatalf("valid ticket rejected: %v", err)
	}
	if keyGeneration := authenticator.AppliedKeyGeneration(); keyGeneration != 3 {
		t.Fatalf("key generation = %d", keyGeneration)
	}
}
