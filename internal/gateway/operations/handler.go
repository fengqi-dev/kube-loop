package operations

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

const (
	LivePath    = "/health/live"
	ReadyPath   = "/health/ready"
	MetricsPath = "/metrics"
)

type GatewayState interface {
	Ready() bool
	Draining() bool
	ActiveConnections() int
}

type WebSocketState interface {
	Draining() bool
	ActiveSessions() int
}

type Handler struct {
	gateway   GatewayState
	websocket WebSocketState
	router    chi.Router
}

func NewHandler(gateway GatewayState, websocket WebSocketState) *Handler {
	handler := &Handler{gateway: gateway, websocket: websocket, router: chi.NewRouter()}
	handler.router.Get(LivePath, handler.live)
	handler.router.Get(ReadyPath, handler.ready)
	handler.router.Get(MetricsPath, handler.metrics)
	return handler
}

func (handler *Handler) Register(router chi.Router) {
	router.Get(LivePath, handler.live)
	router.Get(ReadyPath, handler.ready)
	router.Get(MetricsPath, handler.metrics)
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.router.ServeHTTP(writer, request)
}

func (handler *Handler) live(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *Handler) ready(writer http.ResponseWriter, request *http.Request) {
	ready, status := handler.readiness()
	if !ready {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": status})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (handler *Handler) metrics(writer http.ResponseWriter, request *http.Request) {
	ready, _ := handler.readiness()
	readyValue := 0
	if ready {
		readyValue = 1
	}
	draining := 0
	activeConnections := 0
	activeSessions := 0
	if handler.gateway != nil {
		activeConnections = handler.gateway.ActiveConnections()
		if handler.gateway.Draining() {
			draining = 1
		}
	}
	if handler.websocket != nil {
		activeSessions = handler.websocket.ActiveSessions()
		if handler.websocket.Draining() {
			draining = 1
		}
	}
	writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(writer, "# HELP kubeloop_gateway_ready Whether the data plane can accept new sessions.\n")
	_, _ = fmt.Fprintf(writer, "# TYPE kubeloop_gateway_ready gauge\n")
	_, _ = fmt.Fprintf(writer, "kubeloop_gateway_ready %d\n", readyValue)
	_, _ = fmt.Fprintf(writer, "# HELP kubeloop_gateway_draining Whether the data plane is draining.\n")
	_, _ = fmt.Fprintf(writer, "# TYPE kubeloop_gateway_draining gauge\n")
	_, _ = fmt.Fprintf(writer, "kubeloop_gateway_draining %d\n", draining)
	_, _ = fmt.Fprintf(writer, "# HELP kubeloop_gateway_active_connections Active logical tunnel connections.\n")
	_, _ = fmt.Fprintf(writer, "# TYPE kubeloop_gateway_active_connections gauge\n")
	_, _ = fmt.Fprintf(writer, "kubeloop_gateway_active_connections %d\n", activeConnections)
	_, _ = fmt.Fprintf(writer, "# HELP kubeloop_gateway_active_websocket_sessions Active physical WebSocket sessions.\n")
	_, _ = fmt.Fprintf(writer, "# TYPE kubeloop_gateway_active_websocket_sessions gauge\n")
	_, _ = fmt.Fprintf(writer, "kubeloop_gateway_active_websocket_sessions %d\n", activeSessions)
}

func (handler *Handler) readiness() (bool, string) {
	if handler.gateway == nil || handler.websocket == nil {
		return false, "unavailable"
	}
	if handler.gateway.Draining() || handler.websocket.Draining() {
		return false, "draining"
	}
	if !handler.gateway.Ready() {
		return false, "unavailable"
	}
	return true, "ready"
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
