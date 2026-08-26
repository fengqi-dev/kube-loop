package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/health"
	"github.com/fengqi-dev/kube-loop/internal/testutil/websockettest"
)

type shutdownAllowAuthorizer struct{}

func (shutdownAllowAuthorizer) Authorize(
	context.Context,
	authorization.Subject,
	authorization.Request,
) authorization.Decision {
	return authorization.Decision{Allowed: true}
}

func TestDiscoveryContract(t *testing.T) {
	server, err := NewServer(Config{
		PublicURL:        "https://gateway.example.test/",
		TunnelPath:       "/relay/tunnel",
		ServiceID:        "development",
		MinClientVersion: "2.0.0",
		AuthMethods:      []AuthMethod{{ID: "company", Type: providerOIDC, DisplayName: "Company SSO"}},
	}, BuildInfo{
		Version: "2.0.0-test", Commit: "abc123", ProtocolMin: "2.0", ProtocolMax: "2.0",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, DiscoveryPath, nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=60" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var document DiscoveryDocument
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if document.ServiceID != "development" || document.PublicURL != "https://gateway.example.test" {
		t.Fatalf("unexpected identity: %#v", document)
	}
	if document.TunnelPath != "/relay/tunnel" {
		t.Fatalf("tunnelPath = %q", document.TunnelPath)
	}
	if len(document.APIVersions) != 1 || document.APIVersions[0] != "v2" {
		t.Fatalf("apiVersions = %#v", document.APIVersions)
	}
	if len(document.AuthMethods) != 1 || document.AuthMethods[0].Type != providerOIDC {
		t.Fatalf("authMethods = %#v", document.AuthMethods)
	}
	if document.AuthMethods[0].Interaction != interactionBrowser {
		t.Fatalf("auth interaction = %q", document.AuthMethods[0].Interaction)
	}
	if document.ServerVersion != "2.0.0-test" || document.ServerCommit != "abc123" ||
		document.ProtocolMin != "2.0" ||
		document.ProtocolMax != "2.0" {
		t.Fatalf("unexpected build metadata: %#v", document)
	}
}

func TestDiscoveryAcceptsLocalBrowserAuthentication(t *testing.T) {
	server, err := NewServer(Config{
		PublicURL:   "https://gateway.example.test",
		AuthMethods: []AuthMethod{{ID: providerLocal, Type: providerLocal, DisplayName: "Local account"}},
	}, BuildInfo{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, DiscoveryPath, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var document DiscoveryDocument
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.AuthMethods) != 1 || document.AuthMethods[0].Type != providerLocal ||
		document.AuthMethods[0].Interaction != interactionBrowser {
		t.Fatalf("authMethods = %#v", document.AuthMethods)
	}
}

func TestDiscoveryReadsDynamicAuthenticationMethods(t *testing.T) {
	methods := []AuthMethod{{ID: "first", Type: providerOIDC, Interaction: interactionBrowser}}
	server, err := NewServer(
		Config{PublicURL: "https://gateway.example.test", AuthMethods: methods},
		BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuthMethodSource(
			AuthMethodSourceFunc(
				func() []AuthMethod { return append([]AuthMethod(nil), methods...) },
			),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	read := func() DiscoveryDocument {
		response := httptest.NewRecorder()
		server.Handler().
			ServeHTTP(response, httptest.NewRequest(http.MethodGet, DiscoveryPath, nil))
		if response.Code != http.StatusOK ||
			response.Header().Get("Cache-Control") != "public, max-age=5" {
			t.Fatalf(
				"dynamic discovery status=%d cache=%q",
				response.Code,
				response.Header().Get("Cache-Control"),
			)
		}
		var document DiscoveryDocument
		if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	if got := read().AuthMethods; len(got) != 1 || got[0].ID != "first" {
		t.Fatalf("first discovery methods=%#v", got)
	}
	methods = []AuthMethod{{ID: "second", Type: providerOIDC, Interaction: interactionBrowser}}
	if got := read().AuthMethods; len(got) != 1 || got[0].ID != "second" {
		t.Fatalf("updated discovery methods=%#v", got)
	}
}

func TestServerExposesAdminRoutesOutsideBearerAPI(t *testing.T) {
	server, err := NewServer(
		Config{PublicURL: "https://gateway.example.test"},
		BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAdminRoutes(RouteRegistrarFunc(func(group *echo.Group) {
			group.GET(
				"/status",
				func(ctx *echo.Context) error { return ctx.NoContent(http.StatusNoContent) },
			)
		})),
		WithAPIRoutes(RouteRegistrarFunc(func(group *echo.Group) {
			group.GET(
				"/status",
				func(ctx *echo.Context) error { return ctx.NoContent(http.StatusNoContent) },
			)
		})),
		WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{Subject: "test-user"}, nil
				},
			),
		),
		WithAuthorizer(shutdownAllowAuthorizer{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	adminRequest := httptest.NewRequest(http.MethodGet, AdminPathPrefix+"/status", nil)
	adminResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusNoContent ||
		adminResponse.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("admin status=%d headers=%v", adminResponse.Code, adminResponse.Header())
	}

	legacyResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		legacyResponse,
		httptest.NewRequest(http.MethodGet, APIPathPrefix+"/admin/status", nil),
	)
	if legacyResponse.Code != http.StatusNotFound {
		t.Fatalf("legacy admin status=%d, want %d", legacyResponse.Code, http.StatusNotFound)
	}
}

func TestShutdownCancelsAndWaitsForWebSocketHandlers(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	server, err := NewServer(
		Config{PublicURL: "http://127.0.0.1"},
		BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{Subject: "test-user"}, nil
				},
			),
		),
		WithAuthorizer(shutdownAllowAuthorizer{}),
		WithAPIRoutes(
			testEndpoint(
				func(writer http.ResponseWriter, request *http.Request, _ controlplaneapi.Identity) *controlplaneapi.Error {
					connection, err := websockettest.Accept(writer, request, nil)
					if err != nil {
						return &controlplaneapi.Error{
							Code:    controlplaneapi.CodeInternal,
							Message: err.Error(),
						}
					}
					close(started)
					<-request.Context().Done()
					time.Sleep(20 * time.Millisecond)
					_ = connection.Close()
					close(finished)
					return nil
				},
			),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	connection, _, err := websockettest.Dial(
		context.Background(),
		"ws://"+listener.Addr().String()+"/api/stream",
		nil)

	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("WebSocket handler did not start")
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("Server shutdown returned before WebSocket handler completed")
	}
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}
}

func TestServerRejectsUnsafeTunnelPaths(t *testing.T) {
	for _, tunnelPath := range []string{"https://other.example.test/tunnel", "//other.example.test/tunnel", "/relay/../tunnel", "/tunnel?token=secret"} {
		_, err := NewServer(
			Config{PublicURL: "https://gateway.example.test", TunnelPath: tunnelPath},
			BuildInfo{},
			nil,
		)
		if err == nil {
			t.Fatalf("unsafe tunnel path accepted: %q", tunnelPath)
		}
	}
}

func TestReadinessChecksRequiredDependenciesWithoutLeakingErrors(t *testing.T) {
	checked := false
	server, err := NewServer(
		Config{PublicURL: "https://gateway.example.test", ReadinessTimeout: 100 * time.Millisecond},
		BuildInfo{}, nil,
		WithReadinessChecker(health.CheckFunc(func(ctx context.Context) error {
			checked = true
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("readiness context has no deadline")
			}
			return errors.New("postgres://user:secret@database.internal/kubeloop")
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if !checked || response.Code != http.StatusServiceUnavailable {
		t.Fatalf("checked = %t, status = %d", checked, response.Code)
	}
	if strings.Contains(response.Body.String(), "secret") ||
		strings.Contains(response.Body.String(), "database.internal") {
		t.Fatalf("readiness leaked dependency error: %q", response.Body.String())
	}
}

func TestHealthAndMethodContracts(t *testing.T) {
	server, err := NewServer(Config{PublicURL: "http://127.0.0.1:8080"}, BuildInfo{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, response.Code)
		}
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("GET %s Cache-Control = %q", path, got)
		}
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, DiscoveryPath, nil))
	if response.Code != http.StatusMethodNotAllowed ||
		response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf(
			"POST discovery status = %d, Allow = %q",
			response.Code,
			response.Header().Get("Allow"),
		)
	}
	response = httptest.NewRecorder()
	server.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, DiscoveryPath+"/extra", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("nested discovery status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	server.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/version", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("legacy versioned API path status = %d", response.Code)
	}
}

func TestServerAppliesBoundedRequestHeaders(t *testing.T) {
	server, err := NewServer(Config{PublicURL: "https://gateway.example.test"}, BuildInfo{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if server.http.MaxHeaderBytes != DefaultMaxHeaderBytes || server.http.MaxHeaderBytes > 64<<10 {
		t.Fatalf("MaxHeaderBytes = %d", server.http.MaxHeaderBytes)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []Config{
		{},
		{PublicURL: "gateway.example.test"},
		{PublicURL: "https://user@gateway.example.test"},
		{PublicURL: "https://gateway.example.test?token=secret"},
		{PublicURL: "https://gateway.example.test/team"},
		{PublicURL: "https://gateway.example.test", ServiceID: "bad service"},
		{
			PublicURL:   "https://gateway.example.test",
			AuthMethods: []AuthMethod{{ID: "password", Type: "password"}},
		},
		{
			PublicURL: "https://gateway.example.test",
			AuthMethods: []AuthMethod{
				{ID: "duplicate", Type: providerOIDC},
				{ID: "duplicate", Type: providerOIDC},
			},
		},
		{
			PublicURL:   "https://gateway.example.test",
			AuthMethods: []AuthMethod{{ID: "company", Type: providerOIDC, Interaction: "password"}},
		},
		{
			PublicURL:   "https://gateway.example.test",
			AuthMethods: []AuthMethod{{ID: providerLocal, Type: providerLocal, Interaction: "none"}},
		},
	}
	for _, config := range tests {
		if _, err := NewServer(config, BuildInfo{}, nil); err == nil {
			t.Fatalf("NewServer(%#v) succeeded", config)
		}
	}
	if normalized, err := (Config{PublicURL: "http://gateway.example.test"}).normalized(); err != nil ||
		normalized.PublicURL != "http://gateway.example.test" {
		t.Fatalf("external HTTP public URL was rejected: %#v, %v", normalized, err)
	}
}
