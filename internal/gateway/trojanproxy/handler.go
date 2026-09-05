// Package trojanproxy authenticates the public v3 WebSocket and proxies its
// Upgrade to the loopback sing-box runtime assigned to that Session.
package trojanproxy

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/gateway/websocketmux"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trojanws"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

const DefaultPath = trojanws.DefaultPath

type Resolver interface {
	ResolveTrojanSession(websocketmux.Identity) (*url.URL, error)
}

type Config struct {
	Path          string
	Authenticator websocketmux.Authenticator
	Resolver      Resolver
	Logger        *slog.Logger
	MaxSessions   int
}

type Handler struct {
	config   Config
	limit    chan struct{}
	draining atomic.Bool
}

func (handler *Handler) BeginDrain()         { handler.draining.Store(true) }
func (handler *Handler) Draining() bool      { return handler.draining.Load() }
func (handler *Handler) ActiveSessions() int { return len(handler.limit) }

func NewHandler(config Config) (*Handler, error) {
	if config.Path == "" || config.Path[0] != '/' {
		return nil, errors.New("absolute Trojan WebSocket path is required")
	}
	if config.Authenticator == nil || config.Resolver == nil {
		return nil, errors.New("trojan authenticator and session resolver are required")
	}
	if config.MaxSessions <= 0 {
		config.MaxSessions = 256
	}
	return &Handler{config: config, limit: make(chan struct{}, config.MaxSessions)}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler.draining.Load() {
		writer.Header().Set("Retry-After", "5")
		http.Error(writer, "Gateway is draining", http.StatusServiceUnavailable)
		return
	}
	if request.Method != http.MethodGet || request.URL.Path != handler.config.Path {
		http.NotFound(writer, request)
		return
	}
	identity, err := handler.config.Authenticator.Authenticate(request)
	if err != nil || identity.SessionID == "" || identity.SessionGeneration == 0 ||
		!identity.ExpiresAt.After(time.Now()) {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(writer, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !websocket.IsWebSocketUpgrade(request) {
		http.Error(writer, "WebSocket upgrade is required", http.StatusBadRequest)
		return
	}
	target, err := handler.config.Resolver.ResolveTrojanSession(identity)
	if err != nil || target == nil || target.Scheme != "http" || target.Host == "" {
		if handler.config.Logger != nil {
			handler.config.Logger.WarnContext(request.Context(), "Trojan Session is unavailable", "error", err)
		}
		http.Error(writer, "Session data path is unavailable", http.StatusServiceUnavailable)
		return
	}
	select {
	case handler.limit <- struct{}{}:
		defer func() { <-handler.limit }()
	default:
		http.Error(writer, "Gateway session limit reached", http.StatusServiceUnavailable)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			proxyRequest.SetURL(target)
			proxyRequest.Out.URL.Path = singbox.GatewayWebSocketPath
			proxyRequest.Out.URL.RawPath = ""
			proxyRequest.Out.URL.RawQuery = ""
			proxyRequest.Out.Host = target.Host
		},
		ErrorHandler: func(response http.ResponseWriter, proxyRequest *http.Request, proxyErr error) {
			if handler.config.Logger != nil {
				handler.config.Logger.WarnContext(proxyRequest.Context(), "proxy Trojan WebSocket", "error", proxyErr)
			}
			http.Error(response, "Session data path is unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(writer, request)
}
