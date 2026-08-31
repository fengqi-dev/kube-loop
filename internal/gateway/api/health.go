package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"
)

const (
	LivePath  = "/health/live"
	ReadyPath = "/health/ready"
	statusKey = "status"
)

type GatewayState interface {
	Ready() bool
	Draining() bool
}

type WebSocketState interface {
	Draining() bool
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
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.router.ServeHTTP(writer, request)
}

func (handler *Handler) live(ctx *echo.Context) error {
	ctx.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	return ctx.JSON(http.StatusOK, map[string]string{statusKey: "ok"})
}

func (handler *Handler) ready(ctx *echo.Context) error {
	ctx.Response().Header().Set(echo.HeaderCacheControl, "no-store")
	ready, status := handler.readiness()
	if !ready {
		return ctx.JSON(http.StatusServiceUnavailable, map[string]string{statusKey: status})
	}
	return ctx.JSON(http.StatusOK, map[string]string{statusKey: "ready"})
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
