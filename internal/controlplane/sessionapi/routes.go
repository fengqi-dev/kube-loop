package sessionapi

import (
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/labstack/echo/v5"
)

type Routes struct{ *Service }

func NewRoutes(service *Service) *Routes { return &Routes{Service: service} }

func (handler *Routes) Endpoints() controlplane.SessionEndpoints {
	return controlplane.SessionEndpoints{
		Create:     handler.withNamespace(handler.create),
		Get:        handler.withSession(handler.get),
		Heartbeat:  handler.withSession(handler.heartbeat),
		Disconnect: handler.withSession(handler.disconnect),
	}
}

type namespaceHandler func(*echo.Context, controlplaneapi.Identity, string) *controlplaneapi.Error
type sessionHandler func(*echo.Context, controlplaneapi.Identity, string, string) *controlplaneapi.Error

func (handler *Routes) withNamespace(next namespaceHandler) controlplane.EndpointFunc {
	return func(ctx *echo.Context, identity controlplaneapi.Identity) *controlplaneapi.Error {
		request := ctx.Request()
		namespace, apiError := namespaceFromQuery(request)
		if apiError != nil {
			return apiError
		}
		if apiError := requireEmptyBody(request); apiError != nil {
			return apiError
		}
		return next(ctx, identity, namespace)
	}
}

func (handler *Routes) withSession(next sessionHandler) controlplane.EndpointFunc {
	return handler.withNamespace(func(ctx *echo.Context, identity controlplaneapi.Identity, namespace string) *controlplaneapi.Error {
		return next(ctx, identity, namespace, ctx.Request().PathValue("sessionID"))
	})
}
