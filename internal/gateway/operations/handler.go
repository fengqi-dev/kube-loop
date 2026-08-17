package operations

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
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
	router    *echo.Echo
}

func NewHandler(gateway GatewayState, websocket WebSocketState) *Handler {
	handler := &Handler{gateway: gateway, websocket: websocket, router: echo.New()}
	defaultHTTPErrorHandler := echo.DefaultHTTPErrorHandler(false)
	handler.router.HTTPErrorHandler = func(ctx *echo.Context, err error) {
		if errors.Is(err, echo.ErrMethodNotAllowed) {
			ctx.Response().Header().Set(echo.HeaderAllow, http.MethodGet)
		}
		defaultHTTPErrorHandler(ctx, err)
	}
	handler.Register(handler.router)
	return handler
}

func (handler *Handler) Register(router *echo.Echo) {
	router.GET(LivePath, handler.live)
	router.GET(ReadyPath, handler.ready)
	router.GET(MetricsPath, handler.metrics)
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.router.ServeHTTP(writer, request)
}

func (handler *Handler) live(ctx *echo.Context) error {
	ctx.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return ctx.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *Handler) ready(ctx *echo.Context) error {
	ctx.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	ready, status := handler.readiness()
	if !ready {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{"status": status})
	}
	return ctx.JSON(http.StatusOK, map[string]string{"status": "ready"})
}

func (handler *Handler) metrics(ctx *echo.Context) error {
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
	writer := ctx.Response()
	writer.Header().Set(echo.HeaderContentType, "text/plain; version=0.0.4; charset=utf-8")
	writer.Header().Set(echo.HeaderCacheControl, "no-store")
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
	return nil
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
