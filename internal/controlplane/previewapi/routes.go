package previewapi

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

type sessionHandler func(*echo.Context, controlplaneapi.Identity, sessionapi.ActiveSession) *controlplaneapi.Error
type taskHandler func(*echo.Context, controlplaneapi.Identity, sessionapi.ActiveSession, string) *controlplaneapi.Error

func (handler *Routes) withSession(next sessionHandler) controlplane.EndpointFunc {
	return func(ctx *echo.Context, identity controlplaneapi.Identity) *controlplaneapi.Error {
		request := ctx.Request()
		session, apiError := handler.activeSession(request, identity)
		if apiError != nil {
			return apiError
		}
		return next(ctx, identity, session)
	}
}

func (handler *Routes) withTask(next taskHandler) controlplane.EndpointFunc {
	return handler.withSession(func(ctx *echo.Context, identity controlplaneapi.Identity, session sessionapi.ActiveSession) *controlplaneapi.Error {
		return next(ctx, identity, session, ctx.Request().PathValue("taskID"))
	})
}

func (handler *Routes) activeSession(request *http.Request, identity controlplaneapi.Identity) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	namespace, apiError := namespaceFromQuery(request)
	if apiError != nil {
		return sessionapi.ActiveSession{}, apiError
	}
	session, apiError := handler.sessions.RequireActive(request.Context(), identity, namespace, request.PathValue("sessionID"))
	if apiError != nil {
		return sessionapi.ActiveSession{}, apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	return session, nil
}
