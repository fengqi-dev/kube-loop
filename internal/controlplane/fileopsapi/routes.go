package fileopsapi

import (
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionroute"
)

type Routes struct{ *Service }

func NewRoutes(service *Service) *Routes { return &Routes{Service: service} }

func (handler *Routes) Endpoints() controlplane.FileOperationEndpoints {
	return controlplane.FileOperationEndpoints{
		List:      sessionroute.WithSession(handler.sessions, handler.list),
		Create:    handler.withAction(ActionCreate),
		Rename:    handler.withAction(ActionRename),
		Delete:    handler.withAction(ActionDelete),
		Operation: handler.withTask(handler.get),
	}
}

type taskHandler func(*echo.Context, controlplaneapi.Identity, sessionapi.ActiveSession, string) *controlplaneapi.Error

func (handler *Routes) withAction(action string) controlplane.EndpointFunc {
	return sessionroute.WithSession(
		handler.sessions,
		func(ctx *echo.Context, identity controlplaneapi.Identity, session sessionapi.ActiveSession) *controlplaneapi.Error {
			return handler.mutate(ctx, identity, session, action)
		},
	)
}

func (handler *Routes) withTask(next taskHandler) controlplane.EndpointFunc {
	return sessionroute.WithTask(handler.sessions, sessionroute.TaskHandler(next))
}
