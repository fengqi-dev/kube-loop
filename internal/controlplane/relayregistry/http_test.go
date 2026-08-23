package relayregistry

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

func TestMTLSRegistrationAndHeartbeatUseCertificateIdentity(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 100)
	authenticator, err := NewMTLSAuthenticator(MTLSConfig{
		TrustDomain: "cluster.local", Namespace: "kubeloop-system", ServiceAccount: "kubeloop-data-plane",
		TopologyResolver: func(_ context.Context, identity relaycontrol.PeerIdentity) (map[string]string, error) {
			if identity.PodUID != "pod-a" {
				t.Fatalf("resolved identity = %#v", identity)
			}
			return map[string]string{
				"topology.kubernetes.io/zone": "zone-a",
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	handler, err := NewHTTPHandler(
		registry,
		authenticator,
		slog.New(slog.NewJSONHandler(&logs, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}

	registration := registrationRequest("a.example.test", 100, 0)
	registrationRaw, err := relaycontrol.Encode(registration, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	response := serveInternal(
		t,
		handler,
		http.MethodPost,
		InternalPathPrefix+"/register",
		registrationRaw,
		validTLSState(t),
	)
	if response.Code != http.StatusCreated {
		t.Fatalf(
			"registration status = %d body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	requestID := response.Header().Get("X-Request-ID")
	if requestID == "" ||
		!strings.Contains(logs.String(), `"request_id":"`+requestID+`"`) {
		t.Fatalf(
			"Relay Registry request ID not correlated: header=%q logs=%q",
			requestID,
			logs.String(),
		)
	}
	registered, err := relaycontrol.DecodeRegistrationResponse(
		response.Body.Bytes(),
		clock.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	expectedRelayID, _ := peer("zone-a", "pod-a").RelayID()
	if registered.RelayID != expectedRelayID {
		t.Fatalf(
			"registered Relay ID = %q, want %q",
			registered.RelayID,
			expectedRelayID,
		)
	}

	heartbeat := heartbeatRequest(registered.LeaseID, 100, 1)
	heartbeatRaw, err := relaycontrol.Encode(heartbeat, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	response = serveInternal(
		t,
		handler,
		http.MethodPut,
		InternalPathPrefix+"/heartbeat",
		heartbeatRaw,
		validTLSState(t),
	)
	if response.Code != http.StatusOK {
		t.Fatalf(
			"heartbeat status = %d body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	if _, err := relaycontrol.DecodeHeartbeatResponse(response.Body.Bytes(), clock.Now()); err != nil {
		t.Fatal(err)
	}
	statuses := registry.Snapshot()
	if len(statuses) != 1 ||
		statuses[0].Topology["topology.kubernetes.io/zone"] != "zone-a" {
		t.Fatalf("Relay statuses = %#v", statuses)
	}
}

func TestInternalHandlerRejectsUnverifiedIdentityAndBodyIdentity(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	registry := newTestRegistry(t, clock, 100)
	authenticator, err := NewMTLSAuthenticator(MTLSConfig{
		TrustDomain: "cluster.local", Namespace: "kubeloop-system", ServiceAccount: "kubeloop-data-plane",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHTTPHandler(registry, authenticator, nil)
	if err != nil {
		t.Fatal(err)
	}
	registration := registrationRequest("a.example.test", 100, 0)
	raw, _ := json.Marshal(registration)

	response := serveInternal(
		t,
		handler,
		http.MethodPost,
		InternalPathPrefix+"/register",
		raw,
		nil,
	)
	if response.Code != http.StatusUnauthorized ||
		strings.Contains(response.Body.String(), "certificate") {
		t.Fatalf(
			"unauthenticated response = %d %s",
			response.Code,
			response.Body.String(),
		)
	}
	response = serveInternal(
		t,
		handler,
		http.MethodPost,
		InternalPathPrefix+"/register",
		raw,
		validTLSState(t, "wrong-service-account"),
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong workload identity status = %d", response.Code)
	}

	withRelayID := append(
		append([]byte(nil), raw[:len(raw)-1]...),
		[]byte(`,"relayId":"attacker"}`)...)
	response = serveInternal(
		t,
		handler,
		http.MethodPost,
		InternalPathPrefix+"/register",
		withRelayID,
		validTLSState(t),
	)
	if response.Code != http.StatusBadRequest ||
		strings.Contains(response.Body.String(), "attacker") {
		t.Fatalf(
			"body identity response = %d %s",
			response.Code,
			response.Body.String(),
		)
	}
	if len(registry.Snapshot()) != 0 {
		t.Fatalf(
			"invalid registration mutated Registry: %#v",
			registry.Snapshot(),
		)
	}
}

func TestInternalHandlerMountsRoutes(t *testing.T) {
	clock := &testClock{now: time.Now().UTC().Truncate(time.Second)}
	handler, err := NewHTTPHandler(newTestRegistry(t, clock, 100), testInternalAuthenticator{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.Mount(nil); err == nil {
		t.Fatal("nil internal routes were accepted")
	}
	if err := handler.Mount(testInternalRoutes{}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/internal/test", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("mounted route status = %d", response.Code)
	}
}

func TestRegistryErrorsUseStableHTTPDocuments(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "not found", err: ErrNotFound, status: http.StatusNotFound, code: "not_found"},
		{name: "conflict", err: ErrConflict, status: http.StatusConflict, code: "conflict"},
		{name: "unavailable", err: ErrUnavailable, status: http.StatusServiceUnavailable, code: "unavailable"},
		{
			name: "assigned unavailable", err: ErrAssignedRelayUnavailable,
			status: http.StatusServiceUnavailable, code: "unavailable",
		},
		{
			name: "invalid", err: errors.New("private registry detail"),
			status: http.StatusBadRequest, code: "invalid_argument",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeRegistryError(response, test.err)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("registry error response = %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "private registry detail") {
				t.Fatal("private registry error detail was exposed")
			}
		})
	}
}

type testInternalRoutes struct{}

type testInternalAuthenticator struct{}

func (testInternalAuthenticator) Authenticate(*http.Request) (relaycontrol.PeerIdentity, error) {
	return relaycontrol.PeerIdentity{}, nil
}

func (testInternalRoutes) RegisterRoutes(router *echo.Echo) {
	router.GET("/internal/test", func(ctx *echo.Context) error {
		return ctx.NoContent(http.StatusNoContent)
	})
}

func serveInternal(
	t *testing.T,
	handler http.Handler,
	method, path string,
	body []byte,
	state *tls.ConnectionState,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.TLS = state
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func validTLSState(
	t *testing.T,
	serviceAccounts ...string,
) *tls.ConnectionState {
	t.Helper()
	serviceAccount := "kubeloop-data-plane"
	if len(serviceAccounts) > 0 {
		serviceAccount = serviceAccounts[0]
	}
	identityURI, err := url.Parse(
		"spiffe://cluster.local/ns/kubeloop-system/sa/" + serviceAccount + "/pod/pod-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{URIs: []*url.URL{identityURI}}
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}
}
