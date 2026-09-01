package sessionapi

import (
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/routequery"
)

type Routes struct{ *Service }

func NewRoutes(service *Service) *Routes { return &Routes{Service: service} }

func (handler *Routes) Endpoints() controlplane.SessionEndpoints {
	return controlplane.SessionEndpoints{
		Create:               handler.withNamespace(handler.create),
		Get:                  handler.withSession(handler.get),
		Heartbeat:            handler.withSession(handler.heartbeat),
		Sync:                 handler.withSession(handler.syncTrafficBindings),
		ListTrafficBindings:  handler.withSession(handler.listTrafficBindings),
		DeleteTrafficBinding: handler.withSession(handler.deleteTrafficBinding),
		Disconnect:           handler.withSession(handler.disconnect),
	}
}

type namespaceHandler func(*echo.Context, controlplaneapi.Identity, string) *controlplaneapi.Error

type sessionHandler func(*echo.Context, controlplaneapi.Identity, string, string) *controlplaneapi.Error

func (handler *Routes) withNamespace(next namespaceHandler) controlplane.EndpointFunc {
	return func(ctx *echo.Context, identity controlplaneapi.Identity) *controlplaneapi.Error {
		request := ctx.Request()
		namespace, apiError := routequery.Namespace(request)
		if apiError != nil {
			return apiError
		}
		if apiError := routequery.RequireEmptyBody(request); apiError != nil {
			return apiError
		}
		return next(ctx, identity, namespace)
	}
}

func (handler *Routes) withSession(next sessionHandler) controlplane.EndpointFunc {
	return handler.withNamespace(
		func(ctx *echo.Context, identity controlplaneapi.Identity, namespace string) *controlplaneapi.Error {
			return next(ctx, identity, namespace, ctx.Request().PathValue("sessionID"))
		},
	)
}
