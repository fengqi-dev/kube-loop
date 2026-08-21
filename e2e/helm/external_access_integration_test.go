package helm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
)

// The server also applies WriteTimeout to the TLS handshake. Keep enough room
// for a loaded Windows CI runner while still testing that upgraded WebSockets
// outlive the HTTP response deadline.
const externalProxyWriteTimeout = time.Second

type externalAccessAuthorizer struct{}

func (externalAccessAuthorizer) Authorize(
	context.Context,
	authorization.Subject,
	authorization.Request,
) authorization.Decision {
	return authorization.Decision{Allowed: true}
}

//nolint:gocyclo // This integration test intentionally validates the complete proxy lifecycle in one scenario.
func TestSameOriginTLSProxyPreservesControlPlaneLimitsAndLongLivedWebSocket(t *testing.T) {
	var externalHandler http.Handler
	external := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if externalHandler == nil {
			http.Error(writer, "proxy is starting", http.StatusServiceUnavailable)
			return
		}
		externalHandler.ServeHTTP(writer, request)
	}))
	external.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	external.Config.WriteTimeout = externalProxyWriteTimeout
	publicURL := "https://" + external.Listener.Addr().String()

	controlPlaneServer, err := controlplane.NewServer(
		controlplane.Config{PublicURL: publicURL, MaxRequestBodyBytes: 16},
		controlplane.BuildInfo{Version: "2.0.0-external-test"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
				return controlplaneapi.Identity{Subject: "external-test-user"}, nil
			}),
		),
		controlplane.WithAuthorizer(externalAccessAuthorizer{}),
		controlplane.WithAPIRoutes(controlplane.RouteRegistrarFunc(func(group *echo.Group) {
			group.POST(
				"/body-limit",
				controlplane.Endpoint(func(ctx *echo.Context, _ controlplaneapi.Identity) *controlplaneapi.Error {
					var body struct {
						Name string `json:"name"`
					}
					if err := ctx.Bind(&body); err != nil {
						return controlplanemiddleware.BindingError(err)
					}
					return nil
				}),
			)
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	controlPlaneBackend := httptest.NewServer(markBackend("control-plane", controlPlaneServer.Handler()))
	t.Cleanup(controlPlaneBackend.Close)

	webSocketBackend := httptest.NewServer(
		markBackend("data-plane", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			connection, acceptErr := websocket.Accept(writer, request, nil)
			if acceptErr != nil {
				return
			}
			defer func() { _ = connection.CloseNow() }()
			messageType, payload, readErr := connection.Read(request.Context())
			if readErr != nil {
				return
			}
			_ = connection.Write(request.Context(), messageType, payload)
		})),
	)
	t.Cleanup(webSocketBackend.Close)

	controlPlaneTarget, err := url.Parse(controlPlaneBackend.URL)
	if err != nil {
		t.Fatal(err)
	}
	dataPlaneTarget, err := url.Parse(webSocketBackend.URL)
	if err != nil {
		t.Fatal(err)
	}
	controlPlaneProxy := httputil.NewSingleHostReverseProxy(controlPlaneTarget)
	dataPlaneProxy := httputil.NewSingleHostReverseProxy(dataPlaneTarget)
	externalHandler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == controlplane.DiscoveryPath,
			strings.HasPrefix(request.URL.Path, "/oauth2"),
			strings.HasPrefix(request.URL.Path, controlplane.APIPathPrefix):
			controlPlaneProxy.ServeHTTP(writer, request)
		case request.URL.Path == "/tunnel":
			dataPlaneProxy.ServeHTTP(writer, request)
		default:
			http.NotFound(writer, request)
		}
	})
	external.StartTLS()
	t.Cleanup(external.Close)

	response, err := external.Client().Get(external.URL + controlplane.DiscoveryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.TLS == nil || response.TLS.Version != tls.VersionTLS13 {
		t.Fatalf("TLS discovery status=%d TLS=%#v", response.StatusCode, response.TLS)
	}
	if response.Header.Get("X-Kubeloop-Test-Backend") != "control-plane" {
		t.Fatalf("discovery backend = %q", response.Header.Get("X-Kubeloop-Test-Backend"))
	}
	var discovery controlplane.DiscoveryDocument
	if err := json.NewDecoder(response.Body).Decode(&discovery); err != nil {
		t.Fatal(err)
	}
	if discovery.PublicURL != external.URL || discovery.TunnelPath != "/tunnel" {
		t.Fatalf("discovery external identity = %#v, proxy URL = %q", discovery, external.URL)
	}

	request, err := http.NewRequest(
		http.MethodPost,
		external.URL+controlplane.APIPathPrefix+"/body-limit",
		strings.NewReader(`{"name":"0123456789"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = external.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	limitedBody, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusBadRequest ||
		!strings.Contains(string(limitedBody), "request body exceeds the size limit") {
		t.Fatalf("oversized body status=%d body=%s", response.StatusCode, limitedBody)
	}

	webSocketURL := "wss" + strings.TrimPrefix(external.URL, "https") + "/tunnel"
	connection, upgradeResponse, err := websocket.Dial(context.Background(), webSocketURL, &websocket.DialOptions{
		HTTPClient: external.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.CloseNow() }()
	if upgradeResponse == nil || upgradeResponse.TLS == nil || upgradeResponse.TLS.Version != tls.VersionTLS13 ||
		upgradeResponse.Header.Get("X-Kubeloop-Test-Backend") != "data-plane" {
		t.Fatalf("WSS upgrade response = %#v", upgradeResponse)
	}

	time.Sleep(externalProxyWriteTimeout + 250*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	payload := []byte("long-lived-wss")
	if err := connection.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("write after proxy timeout: %v", err)
	}
	messageType, echoed, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read after proxy timeout: %v", err)
	}
	if messageType != websocket.MessageBinary || string(echoed) != string(payload) {
		t.Fatalf("WSS echo type=%v payload=%q", messageType, echoed)
	}
}

func markBackend(name string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Kubeloop-Test-Backend", name)
		next.ServeHTTP(writer, request)
	})
}
