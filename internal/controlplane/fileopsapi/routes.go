package fileopsapi

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
)

type Routes struct{ *Service }

func NewRoutes(service *Service) *Routes { return &Routes{Service: service} }

func (handler *Routes) Endpoints() controlplane.FileOperationEndpoints {
	return controlplane.FileOperationEndpoints{
		List:      handler.withSession(handler.list),
		Create:    handler.withAction(ActionCreate),
		Rename:    handler.withAction(ActionRename),
		Delete:    handler.withAction(ActionDelete),
		Operation: handler.withTask(handler.get),
	}
}

type sessionHandler func(*echo.Context, controlplaneapi.Identity, sessionapi.ActiveSession) *controlplaneapi.Error

type taskHandler func(*echo.Context, controlplaneapi.Identity, sessionapi.ActiveSession, string) *controlplaneapi.Error

func (handler *Routes) withSession(
	next sessionHandler,
) controlplane.EndpointFunc {
	return func(ctx *echo.Context, identity controlplaneapi.Identity) *controlplaneapi.Error {
		request := ctx.Request()
		session, apiError := handler.activeSession(request, identity)
		if apiError != nil {
			return apiError
		}
		return next(ctx, identity, session)
	}
}

func (handler *Routes) withAction(action string) controlplane.EndpointFunc {
	return handler.withSession(
		func(ctx *echo.Context, identity controlplaneapi.Identity, session sessionapi.ActiveSession) *controlplaneapi.Error {
			return handler.mutate(ctx, identity, session, action)
		},
	)
}

func (handler *Routes) withTask(next taskHandler) controlplane.EndpointFunc {
	return handler.withSession(
		func(ctx *echo.Context, identity controlplaneapi.Identity, session sessionapi.ActiveSession) *controlplaneapi.Error {
			return next(
				ctx,
				identity,
				session,
				ctx.Request().PathValue("taskID"),
			)
		},
	)
}

func (handler *Routes) activeSession(
	request *http.Request,
	identity controlplaneapi.Identity,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	namespace, apiError := namespaceFromQuery(request)
	if apiError != nil {
		return sessionapi.ActiveSession{}, apiError
	}
	session, apiError := handler.sessions.RequireActive(
		request.Context(),
		identity,
		namespace,
		request.PathValue("sessionID"),
	)
	if apiError != nil {
		return sessionapi.ActiveSession{}, apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), session.ID)
	return session, nil
}
