package relayregistry

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/httpmiddleware"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

const InternalPathPrefix = "/internal/v1/relays"

type Authenticator interface {
	Authenticate(*http.Request) (relaycontrol.PeerIdentity, error)
}

type HTTPHandler struct {
	registry      *Registry
	authenticator Authenticator
	router        *echo.Echo
}

func NewHTTPHandler(
	registry *Registry,
	authenticator Authenticator,
	logger *slog.Logger,
) (*HTTPHandler, error) {
	if registry == nil || authenticator == nil {
		return nil, errors.New("relay registry and authenticator are required")
	}
	handler := &HTTPHandler{registry: registry, authenticator: authenticator}
	router := echo.New()
	router.Use(httpmiddleware.RequestID())
	router.Use(httpmiddleware.RequestLogger(logger))
	group := router.Group(InternalPathPrefix)
	group.POST("/register", handler.register)
	group.PUT("/heartbeat", handler.heartbeat)
	handler.router = router
	return handler, nil
}

func (handler *HTTPHandler) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	handler.router.ServeHTTP(writer, request)
}

// Mount adds authenticated internal APIs to the Relay control server. It must
// be called during startup, before ServeHTTP is used.
func (handler *HTTPHandler) Mount(
	routes interface{ RegisterRoutes(*echo.Echo) },
) error {
	if handler == nil || handler.router == nil || routes == nil {
		return errors.New("internal API routes are required")
	}
	routes.RegisterRoutes(handler.router)
	return nil
}

func (handler *HTTPHandler) register(ctx *echo.Context) error {
	return handleInternalRequest(
		handler,
		ctx,
		"Relay registration body is invalid",
		"Relay registration is invalid",
		http.StatusCreated,
		relaycontrol.DecodeRegistrationRequest,
		handler.registry.Register,
	)
}

func (handler *HTTPHandler) heartbeat(ctx *echo.Context) error {
	return handleInternalRequest(
		handler,
		ctx,
		"Relay heartbeat body is invalid",
		"Relay heartbeat is invalid",
		http.StatusOK,
		relaycontrol.DecodeHeartbeatRequest,
		handler.registry.Heartbeat,
	)
}
