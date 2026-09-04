package ticketapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	ticketservice "github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi/service"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type fakeAllocator struct {
	request  relaycontrol.AllocationRequest
	response relaycontrol.AllocationResponse
	err      error
}

func (allocator *fakeAllocator) Allocate(
	request relaycontrol.AllocationRequest,
) (relaycontrol.AllocationResponse, error) {
	allocator.request = request
	return allocator.response, allocator.err
}

type fakeSessions struct {
	binding      sessionapi.ActiveSession
	apiError     *controlplaneapi.Error
	identity     controlplaneapi.Identity
	namespace    string
	sessionID    string
	validateCall int
}

func (sessions *fakeSessions) RequireActive(
	_ context.Context,
	identity controlplaneapi.Identity,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	sessions.validateCall++
	sessions.identity = identity
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
	handler := newTicketRoutes(t, sessions, ticketservice.Config{
		Issuer: "https://controlplane.example", TTL: time.Minute,
		Now: func() time.Time { return now }, Signer: signer,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		controlplane.APIPathPrefix+"/sessions/"+sessionID+"/tickets?namespace=development",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	identity := controlplaneapi.Identity{
		Subject:  "11111111-1111-4111-8111-111111111111",
		DeviceID: "22222222-2222-4222-8222-222222222222",
		Groups:   []string{"developers"},
	}
	if apiError := serveTicketHandler(handler, response, request, identity); apiError != nil {
		t.Fatalf("issue error = %v", apiError)
	}
	if response.Code != http.StatusCreated {
		t.Fatalf(
			"status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	var document issueResponse
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.TokenType != relayticket.Type ||
		document.DeviceID != identity.DeviceID ||
		!document.ExpiresAt.Equal(now.Add(45*time.Second)) {
		t.Fatalf("response = %#v", document)
	}
	verifier, err := relayticket.NewVerifier(relayticket.VerifierConfig{
		Keys: map[string]ed25519.PublicKey{
			"primary": publicKey,
		},
		Issuer:            "https://controlplane.example",
		Audience:          "relay-a",
		RequiredOperation: ticketservice.OperationTunnel,
		Now:               func() time.Time { return now.Add(time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(document.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if claims.IdentityID != identity.Subject || claims.DeviceID != identity.DeviceID ||
		claims.SessionID != sessionID || claims.SessionGeneration != 7 || claims.Namespace != "development" ||
		claims.NetworkSpecHash != strings.Repeat(
			"a",
			64,
		) || len(claims.Groups) != 1 ||
		claims.Groups[0] != "developers" {
		t.Fatalf("claims = %#v", claims)
	}
	if sessions.validateCall != 1 || sessions.identity.Subject != identity.Subject ||
		sessions.namespace != "development" ||
		sessions.sessionID != sessionID {
		t.Fatalf("session validation = %#v", sessions)
	}
}

func TestIssueRelayTicketUsesRegistryAssignment(t *testing.T) {
	const correlationID = "55555555-5555-4555-8555-555555555555"
	var logs bytes.Buffer
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
		TrafficEncryption: true, NoisePublicKey: "test-noise-public-key",
	}}
	handler := newTicketRoutes(
		t,
		&fakeSessions{binding: sessionapi.ActiveSession{
			ID: sessionID, Namespace: "development", Generation: 7, ExpiresAt: now.Add(time.Minute),
			NetworkSpecHash: strings.Repeat("b", 64),
		}},
		ticketservice.Config{
			Issuer: "https://controlplane.example", TTL: time.Minute, Now: func() time.Time { return now },
			Signer: signer, Allocator: allocator, Topology: map[string]string{"topology.kubernetes.io/zone": "cn-a"},
			Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
		},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		controlplane.APIPathPrefix+"/sessions/"+sessionID+"/tickets?namespace=development",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(utils.WithCorrelationID(request.Context(), correlationID))
	response := httptest.NewRecorder()
	if apiError := serveTicketHandler(handler, response, request, controlplaneapi.Identity{
		Subject: "11111111-1111-4111-8111-111111111111", DeviceID: "22222222-2222-4222-8222-222222222222",
	}); apiError != nil {
		t.Fatalf("issue error = %v", apiError)
	}
	var document issueResponse
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.DeviceID != "22222222-2222-4222-8222-222222222222" ||
		document.RelayID != relayID || document.Endpoint != "wss://relay.example/tunnel" {
		t.Fatalf("response = %#v", document)
	}
	if allocator.request.SessionID != sessionID ||
		allocator.request.Generation != 7 ||
		allocator.request.NetworkSpecHash != strings.Repeat("b", 64) ||
		allocator.request.Topology["topology.kubernetes.io/zone"] != "cn-a" {
		t.Fatalf("allocation = %#v", allocator.request)
	}
	verifier, err := relayticket.NewVerifier(relayticket.VerifierConfig{
		Keys: map[string]ed25519.PublicKey{
			"primary": publicKey,
		},
		Issuer:            "https://controlplane.example",
		Audience:          relayID,
		RequiredOperation: ticketservice.OperationTunnel,
		Now:               func() time.Time { return now.Add(time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(document.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), `"correlation_id":"`+correlationID+`"`) ||
		!strings.Contains(logs.String(), `"ticket_id":"`+claims.TicketID+`"`) ||
		strings.Contains(logs.String(), document.Ticket) {
		t.Fatalf("RelayTicket log = %q", logs.String())
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
	handler := newTicketRoutes(t, sessions, ticketservice.Config{
		Issuer: "https://controlplane.example", Signer: signer,
	})
	tests := []struct {
		name        string
		url         string
		body        string
		contentType string
	}{
		{
			name:        "bad session",
			url:         controlplane.APIPathPrefix + "/sessions/not-a-uuid/tickets?namespace=development",
			body:        `{}`,
			contentType: "application/json",
		},
		{
			name:        "bad namespace",
			url:         controlplane.APIPathPrefix + "/sessions/33333333-3333-4333-8333-333333333333/tickets?namespace=Bad",
			body:        `{}`,
			contentType: "application/json",
		},
		{
			name:        "wrong media type",
			url:         controlplane.APIPathPrefix + "/sessions/33333333-3333-4333-8333-333333333333/tickets?namespace=development",
			body:        `{}`,
			contentType: "text/plain",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				test.url,
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", test.contentType)
			apiError := serveTicketHandler(
				handler,
				httptest.NewRecorder(),
				request,
				controlplaneapi.Identity{
					Subject: "11111111-1111-4111-8111-111111111111", DeviceID: "22222222-2222-4222-8222-222222222222",
				},
			)
			if apiError == nil ||
				(apiError.Code != controlplaneapi.CodeInvalidArgument && apiError.Code != controlplaneapi.CodeNotFound) {
				t.Fatalf("issue error = %#v", apiError)
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
	sessions := &fakeSessions{
		apiError: &controlplaneapi.Error{
			Code:    controlplaneapi.CodeNotFound,
			Message: resourceNotFoundMessage,
		},
	}
	handler := newTicketRoutes(t, sessions, ticketservice.Config{
		Issuer: "https://controlplane.example", Signer: signer,
	})
	request := httptest.NewRequest(
		http.MethodPost,
		controlplane.APIPathPrefix+"/sessions/33333333-3333-4333-8333-333333333333/tickets?namespace=development",
		strings.NewReader(`{}`),
	)
	request.Header.Set("Content-Type", "application/json")
	apiError := serveTicketHandler(
		handler,
		httptest.NewRecorder(),
		request,
		controlplaneapi.Identity{
			Subject: "foreign", DeviceID: "foreign-device",
		},
	)
	if apiError == nil || apiError.Code != controlplaneapi.CodeNotFound ||
		apiError.Message != resourceNotFoundMessage {
		t.Fatalf("issue error = %#v", apiError)
	}
}

func serveTicketHandler(
	handler *Routes,
	writer http.ResponseWriter,
	request *http.Request,
	identity controlplaneapi.Identity,
) *controlplaneapi.Error {
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) >= 3 {
		request.SetPathValue("sessionID", parts[2])
	}
	return handler.issue(echo.New().NewContext(request, writer), identity)
}

func newTicketRoutes(
	t *testing.T,
	sessions SessionValidator,
	config ticketservice.Config,
) *Routes {
	t.Helper()
	if config.Allocator == nil {
		config.Allocator = &fakeAllocator{
			response: relaycontrol.AllocationResponse{
				RelayID: "relay-a", TrafficEncryption: true,
				NoisePublicKey: "test-noise-public-key",
			},
		}
	}
	service, err := ticketservice.New(config)
	if err != nil {
		t.Fatal(err)
	}
	return NewRoutes(service, sessions)
}
