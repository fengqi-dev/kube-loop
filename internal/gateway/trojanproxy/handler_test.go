package trojanproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

type resolverFunc func(websocketmux.Identity) (*url.URL, error)

func (function resolverFunc) ResolveTrojanSession(identity websocketmux.Identity) (*url.URL, error) {
	return function(identity)
}

func TestHandlerAuthenticatesAndProxiesWebSocket(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != singbox.GatewayWebSocketPath {
			http.NotFound(writer, request)
			return
		}
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		messageType, payload, err := connection.ReadMessage()
		if err == nil {
			_ = connection.WriteMessage(messageType, payload)
		}
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	handler, err := NewHandler(Config{
		Path: DefaultPath,
		Authenticator: websocketmux.AuthenticatorFunc(func(request *http.Request) (websocketmux.Identity, error) {
			if request.Header.Get("Authorization") != "Bearer fresh-ticket" {
				return websocketmux.Identity{}, errors.New("invalid ticket")
			}
			return websocketmux.Identity{
				SessionID: "33333333-3333-4333-8333-333333333333", SessionGeneration: 7,
				ExpiresAt: time.Now().Add(time.Minute),
			}, nil
		}),
		Resolver: resolverFunc(func(identity websocketmux.Identity) (*url.URL, error) {
			if identity.SessionGeneration != 7 {
				return nil, errors.New("wrong Session")
			}
			return target, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	endpoint := "ws" + server.URL[len("http"):] + DefaultPath
	header := http.Header{"Authorization": []string{"Bearer fresh-ticket"}}
	connection, _, err := websocket.DefaultDialer.DialContext(context.Background(), endpoint, header)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := connection.WriteMessage(websocket.BinaryMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, payload, err := connection.ReadMessage()
	if err != nil || string(payload) != "ping" {
		t.Fatalf("proxied response = %q, %v", payload, err)
	}
}

func TestHandlerRejectsUnauthorizedRequestBeforeResolve(t *testing.T) {
	resolved := false
	handler, err := NewHandler(Config{
		Path: DefaultPath,
		Authenticator: websocketmux.AuthenticatorFunc(func(*http.Request) (websocketmux.Identity, error) {
			return websocketmux.Identity{}, errors.New("invalid ticket")
		}),
		Resolver: resolverFunc(func(websocketmux.Identity) (*url.URL, error) {
			resolved = true
			return nil, errors.New("unexpected")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, DefaultPath, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || resolved {
		t.Fatalf("status=%d resolved=%v", response.Code, resolved)
	}
}

func TestHandlerRejectsNonWebSocketRequestBeforeResolve(t *testing.T) {
	resolved := false
	handler, err := NewHandler(Config{
		Path: DefaultPath,
		Authenticator: websocketmux.AuthenticatorFunc(func(*http.Request) (websocketmux.Identity, error) {
			return websocketmux.Identity{
				SessionID: "33333333-3333-4333-8333-333333333333", SessionGeneration: 7,
				ExpiresAt: time.Now().Add(time.Minute),
			}, nil
		}),
		Resolver: resolverFunc(func(websocketmux.Identity) (*url.URL, error) {
			resolved = true
			return nil, errors.New("unexpected")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, DefaultPath, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || resolved {
		t.Fatalf("status=%d resolved=%v", response.Code, resolved)
	}
}
