package ticketapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/go-chi/chi/v5"
)

type fakeAllocator struct {
	request  relaycontrol.AllocationRequest
	response relaycontrol.AllocationResponse
	err      error
}

func (allocator *fakeAllocator) Allocate(request relaycontrol.AllocationRequest) (relaycontrol.AllocationResponse, error) {
	allocator.request = request
	return allocator.response, allocator.err
}

type fakeSessions struct {
	binding      sessionapi.ActiveSession
	apiError     *controller.APIError
	principal    controller.Principal
	namespace    string
	sessionID    string
	validateCall int
}

func (sessions *fakeSessions) RequireActive(
	_ context.Context,
	principal controller.Principal,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controller.APIError) {
	sessions.validateCall++
	sessions.principal = principal
	sessions.namespace = namespace
	sessions.sessionID = sessionID
	return sessions.binding, sessions.apiError
}

func TestIssueRelayTicketIsBoundToActiveSession(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := relayticket.NewSigner("primary", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "33333333-3333-4333-8333-333333333333"
	sessions := &fakeSessions{binding: sessionapi.ActiveSession{
		ID: sessionID, Namespace: "development", Generation: 7, ExpiresAt: now.Add(45 * time.Second),
		NetworkSpecHash: strings.Repeat("a", 64),
	}}
	handler, err := New(sessions, Config{
		Issuer: "https://controller.example", Audience: "relay-a", TTL: time.Minute,
		Now: func() time.Time { return now }, Signer: signer,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		controller.APIPathPrefix+"/sessions/"+sessionID+"/tickets?namespace=development",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	principal := controller.Principal{
		Subject:  "11111111-1111-4111-8111-111111111111",
		DeviceID: "22222222-2222-4222-8222-222222222222",
	}
	if apiError := serveTicketHandler(handler, response, request, principal); apiError != nil {
		t.Fatalf("ServeAPI error = %v", apiError)
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var document issueResponse
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.TokenType != relayticket.Type || document.DeviceID != principal.DeviceID ||
		!document.ExpiresAt.Equal(now.Add(45*time.Second)) {
		t.Fatalf("response = %#v", document)
	}
	verifier, err := relayticket.NewVerifier(relayticket.VerifierConfig{
		Keys: map[string]ed25519.PublicKey{"primary": publicKey}, Issuer: "https://controller.example",
		Audience: "relay-a", RequiredOperation: OperationTunnel, Now: func() time.Time { return now.Add(time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(document.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if claims.PrincipalID != principal.Subject || claims.DeviceID != principal.DeviceID ||
		claims.SessionID != sessionID || claims.SessionGeneration != 7 || claims.Namespace != "development" ||
		claims.NetworkSpecHash != strings.Repeat("a", 64) {
		t.Fatalf("claims = %#v", claims)
	}
	if sessions.validateCall != 1 || sessions.principal.Subject != principal.Subject ||
		sessions.namespace != "development" || sessions.sessionID != sessionID {
		t.Fatalf("session validation = %#v", sessions)
	}
}

func TestIssueRelayTicketUsesRegistryAssignment(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := relayticket.NewSigner("primary", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "33333333-3333-4333-8333-333333333333"
	relayID := "relay-" + strings.Repeat("a", 64)
	allocator := &fakeAllocator{response: relaycontrol.AllocationResponse{
		Envelope: relaycontrol.NewAllocationResponse().Envelope,
		RelayID:  relayID, LeaseID: "44444444-4444-4444-8444-444444444444",
		Endpoint: "wss://relay.example/tunnel", AssignedAt: now,
	}}
	handler, err := New(&fakeSessions{binding: sessionapi.ActiveSession{
		ID: sessionID, Namespace: "development", Generation: 7, ExpiresAt: now.Add(time.Minute),
		NetworkSpecHash: strings.Repeat("b", 64),
	}}, Config{
		Issuer: "https://controller.example", TTL: time.Minute, Now: func() time.Time { return now },
		Signer: signer, Allocator: allocator, Topology: map[string]string{"topology.kubernetes.io/zone": "cn-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		controller.APIPathPrefix+"/sessions/"+sessionID+"/tickets?namespace=development",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	if apiError := serveTicketHandler(handler, response, request, controller.Principal{
		Subject: "11111111-1111-4111-8111-111111111111", DeviceID: "22222222-2222-4222-8222-222222222222",
	}); apiError != nil {
		t.Fatalf("ServeAPI error = %v", apiError)
	}
	var document issueResponse
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.DeviceID != "22222222-2222-4222-8222-222222222222" ||
		document.RelayID != relayID || document.Endpoint != "wss://relay.example/tunnel" {
		t.Fatalf("response = %#v", document)
	}
	if allocator.request.SessionID != sessionID || allocator.request.Generation != 7 ||
		allocator.request.NetworkSpecHash != strings.Repeat("b", 64) ||
		allocator.request.Topology["topology.kubernetes.io/zone"] != "cn-a" {
		t.Fatalf("allocation = %#v", allocator.request)
	}
	verifier, err := relayticket.NewVerifier(relayticket.VerifierConfig{
		Keys: map[string]ed25519.PublicKey{"primary": publicKey}, Issuer: "https://controller.example",
		Audience: relayID, RequiredOperation: OperationTunnel, Now: func() time.Time { return now.Add(time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(document.Ticket); err != nil {
		t.Fatal(err)
	}
}

func TestIssueRelayTicketRejectsInvalidInputBeforeSessionLookup(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := relayticket.NewSigner("primary", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	sessions := &fakeSessions{}
	handler, err := New(sessions, Config{
		Issuer: "https://controller.example", Audience: "relay-a", Signer: signer,
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		url         string
		body        string
		contentType string
	}{
		{name: "bad session", url: controller.APIPathPrefix + "/sessions/not-a-uuid/tickets?namespace=development", body: `{}`, contentType: "application/json"},
		{name: "bad namespace", url: controller.APIPathPrefix + "/sessions/33333333-3333-4333-8333-333333333333/tickets?namespace=Bad", body: `{}`, contentType: "application/json"},
		{name: "bad hash", url: controller.APIPathPrefix + "/sessions/33333333-3333-4333-8333-333333333333/tickets?namespace=development", body: `{"networkSpecHash":"ABC"}`, contentType: "application/json"},
		{name: "unknown JSON", url: controller.APIPathPrefix + "/sessions/33333333-3333-4333-8333-333333333333/tickets?namespace=development", body: `{"unknown":true}`, contentType: "application/json"},
		{name: "wrong media type", url: controller.APIPathPrefix + "/sessions/33333333-3333-4333-8333-333333333333/tickets?namespace=development", body: `{}`, contentType: "text/plain"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.url, strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			apiError := serveTicketHandler(handler, httptest.NewRecorder(), request, controller.Principal{
				Subject: "11111111-1111-4111-8111-111111111111", DeviceID: "22222222-2222-4222-8222-222222222222",
			})
			if apiError == nil || (apiError.Code != controller.CodeInvalidArgument && apiError.Code != controller.CodeNotFound) {
				t.Fatalf("ServeAPI error = %#v", apiError)
			}
		})
	}
	if sessions.validateCall != 0 {
		t.Fatalf("session validator called %d times", sessions.validateCall)
	}
}

func TestIssueRelayTicketDoesNotLeakSessionValidationDetails(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := relayticket.NewSigner("primary", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	sessions := &fakeSessions{apiError: &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}}
	handler, err := New(sessions, Config{Issuer: "https://controller.example", Audience: "relay-a", Signer: signer})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		controller.APIPathPrefix+"/sessions/33333333-3333-4333-8333-333333333333/tickets?namespace=development",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	apiError := serveTicketHandler(handler, httptest.NewRecorder(), request, controller.Principal{
		Subject: "foreign", DeviceID: "foreign-device",
	})
	if apiError == nil || apiError.Code != controller.CodeNotFound || apiError.Message != "resource not found" {
		t.Fatalf("ServeAPI error = %#v", apiError)
	}
}

func serveTicketHandler(
	handler *Handler,
	writer http.ResponseWriter,
	request *http.Request,
	principal controller.Principal,
) *controller.APIError {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	routeContext := chi.NewRouteContext()
	if len(parts) >= 4 {
		routeContext.URLParams.Add("sessionID", parts[3])
	}
	request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
	return handler.ServeAPI(writer, request, principal)
}
