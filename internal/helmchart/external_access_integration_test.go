package helmchart

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

	"github.com/coder/websocket"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
)

const externalProxyWriteTimeout = 50 * time.Millisecond

type externalAccessAuthorizer struct{}

func (externalAccessAuthorizer) Authorize(
	context.Context,
	authorization.Subject,
	authorization.Request,
) authorization.Decision {
	return authorization.Decision{Allowed: true, RuleID: "external-access-test"}
}

func TestSameOriginTLSProxyPreservesControllerLimitsAndLongLivedWebSocket(t *testing.T) {
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

	controllerServer, err := controller.NewServer(
		controller.Config{PublicURL: publicURL, MaxRequestBodyBytes: 16},
		controller.BuildInfo{Version: "2.0.0-external-test"},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controller.WithAuthenticator(controller.AuthenticatorFunc(func(*http.Request) (controller.Principal, *controller.APIError) {
			return controller.Principal{Subject: "external-test-user"}, nil
		})),
		controller.WithAuthorizer(externalAccessAuthorizer{}),
		controller.WithAPIHandler(controller.APIHandlerFunc(func(
			_ http.ResponseWriter,
			request *http.Request,
			_ controller.Principal,
		) *controller.APIError {
			var body struct {
				Name string `json:"name"`
			}
			return controller.DecodeJSON(request, &body)
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	controllerBackend := httptest.NewServer(markBackend("controller", controllerServer.Handler()))
	t.Cleanup(controllerBackend.Close)

	webSocketBackend := httptest.NewServer(markBackend("data-plane", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, acceptErr := websocket.Accept(writer, request, nil)
		if acceptErr != nil {
			return
		}
		defer connection.CloseNow()
		messageType, payload, readErr := connection.Read(request.Context())
		if readErr != nil {
			return
		}
		_ = connection.Write(request.Context(), messageType, payload)
	})))
	t.Cleanup(webSocketBackend.Close)

	controllerTarget, err := url.Parse(controllerBackend.URL)
	if err != nil {
		t.Fatal(err)
	}
	dataPlaneTarget, err := url.Parse(webSocketBackend.URL)
	if err != nil {
		t.Fatal(err)
	}
	controllerProxy := httputil.NewSingleHostReverseProxy(controllerTarget)
	dataPlaneProxy := httputil.NewSingleHostReverseProxy(dataPlaneTarget)
	externalHandler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == controller.DiscoveryPath,
			strings.HasPrefix(request.URL.Path, "/auth"),
			strings.HasPrefix(request.URL.Path, controller.APIPathPrefix):
			controllerProxy.ServeHTTP(writer, request)
		case request.URL.Path == "/tunnel":
			dataPlaneProxy.ServeHTTP(writer, request)
		default:
			http.NotFound(writer, request)
		}
	})
	external.StartTLS()
	t.Cleanup(external.Close)

	response, err := external.Client().Get(external.URL + controller.DiscoveryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.TLS == nil || response.TLS.Version != tls.VersionTLS13 {
		t.Fatalf("TLS discovery status=%d TLS=%#v", response.StatusCode, response.TLS)
	}
	if response.Header.Get("X-KubeLoop-Test-Backend") != "controller" {
		t.Fatalf("discovery backend = %q", response.Header.Get("X-KubeLoop-Test-Backend"))
	}
	var discovery controller.DiscoveryDocument
	if err := json.NewDecoder(response.Body).Decode(&discovery); err != nil {
		t.Fatal(err)
	}
	if discovery.PublicURL != external.URL || discovery.TunnelPath != "/tunnel" {
		t.Fatalf("discovery external identity = %#v, proxy URL = %q", discovery, external.URL)
	}

	request, err := http.NewRequest(http.MethodPost, external.URL+controller.APIPathPrefix+"/body-limit", strings.NewReader(`{"name":"0123456789"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err = external.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	limitedBody, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusBadRequest || !strings.Contains(string(limitedBody), "request body exceeds the size limit") {
		t.Fatalf("oversized body status=%d body=%s", response.StatusCode, limitedBody)
	}

	webSocketURL := "wss" + strings.TrimPrefix(external.URL, "https") + "/tunnel"
	connection, upgradeResponse, err := websocket.Dial(context.Background(), webSocketURL, &websocket.DialOptions{
		HTTPClient: external.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if upgradeResponse == nil || upgradeResponse.TLS == nil || upgradeResponse.TLS.Version != tls.VersionTLS13 ||
		upgradeResponse.Header.Get("X-KubeLoop-Test-Backend") != "data-plane" {
		t.Fatalf("WSS upgrade response = %#v", upgradeResponse)
	}

	time.Sleep(4 * externalProxyWriteTimeout)
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
		writer.Header().Set("X-KubeLoop-Test-Backend", name)
		next.ServeHTTP(writer, request)
	})
}
