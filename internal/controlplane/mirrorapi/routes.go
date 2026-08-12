package mirrorapi

import (
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/labstack/echo/v5"
)

type Routes struct{ *Service }

func NewRoutes(service *Service) *Routes { return &Routes{Service: service} }

func (handler *Routes) Endpoints() controlplane.RemoteTaskEndpoints {
	return controlplane.RemoteTaskEndpoints{
		Create: handler.withSession(handler.create),
		Get:    handler.withTask(handler.get),
		Stop:   handler.withTask(handler.stop),
	}
}

type sessionHandler func(*echo.Context, controlplaneapi.Principal, sessionapi.ActiveSession) *controlplaneapi.Error
type taskHandler func(*echo.Context, controlplaneapi.Principal, sessionapi.ActiveSession, string) *controlplaneapi.Error

func (handler *Routes) withSession(next sessionHandler) controlplane.EndpointFunc {
	return func(ctx *echo.Context, principal controlplaneapi.Principal) *controlplaneapi.Error {
		request := ctx.Request()
		session, apiError := handler.activeSession(request, principal)
		if apiError != nil {
			return apiError
		}
		return next(ctx, principal, session)
	}
}

func (handler *Routes) withTask(next taskHandler) controlplane.EndpointFunc {
	return handler.withSession(func(ctx *echo.Context, principal controlplaneapi.Principal, session sessionapi.ActiveSession) *controlplaneapi.Error {
		return next(ctx, principal, session, ctx.Request().PathValue("taskID"))
	})
}

func (handler *Routes) activeSession(request *http.Request, principal controlplaneapi.Principal) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	namespace, apiError := namespaceFromQuery(request)
	if apiError != nil {
		return sessionapi.ActiveSession{}, apiError
	}
	session, apiError := handler.sessions.RequireActive(request.Context(), principal, namespace, request.PathValue("sessionID"))
	if apiError != nil {
		return sessionapi.ActiveSession{}, apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	return session, nil
}
