package portforwardapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/routequery"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionroute"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
)

type SessionValidator interface {
	RequireActive(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
	) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type Routes struct {
	service  *portforwardservice.Service
	sessions SessionValidator
}

func NewRoutes(
	service *portforwardservice.Service,
	sessions SessionValidator,
) *Routes {
	return &Routes{service: service, sessions: sessions}
}

func (routes *Routes) Endpoints() controlplane.PortForwardEndpoints {
	return controlplane.PortForwardEndpoints{
		Create: sessionroute.WithSessionResolver(routes.sessions, namespaceFromQuery, routes.create),
		List:   sessionroute.WithSessionResolver(routes.sessions, namespaceFromQuery, routes.list),
		Pause:  sessionroute.WithTaskResolver(routes.sessions, namespaceFromQuery, routes.pause),
		Resume: sessionroute.WithTaskResolver(routes.sessions, namespaceFromQuery, routes.resume),
		Delete: sessionroute.WithTaskResolver(routes.sessions, namespaceFromQuery, routes.delete),
		Stop:   sessionroute.WithTaskResolver(routes.sessions, namespaceFromQuery, routes.pause),
	}
}

func (routes *Routes) create(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	var spec portforwardservice.Spec
	if err := ctx.Bind(&spec); err != nil {
		return controlplanemiddleware.BindingError(err)
	}
	idempotencyKey, apiError := taskapi.IdempotencyKey(ctx.Request())
	if apiError != nil {
		return apiError
	}
	result, apiError := routes.service.Create(
		ctx.Request().Context(), identity, session, spec, idempotencyKey,
	)
	if apiError != nil {
		return apiError
	}
	ctx.Response().Header().Set("Location", fmt.Sprintf(
		"%s/sessions/%s/port-forwards/%s?namespace=%s",
		controlplane.APIPathPrefix,
		session.ID,
		result.PortForward.ID,
		session.Namespace,
	))
	if result.Replayed {
		ctx.Response().Header().Set("Idempotent-Replayed", "true")
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	_ = ctx.JSON(status, documentFromEntity(result.PortForward))
	return nil
}

func (routes *Routes) list(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
) *controlplaneapi.Error {
	if apiError := routequery.RequireEmptyBody(ctx.Request()); apiError != nil {
		return apiError
	}
	portForwards, apiError := routes.service.List(
		ctx.Request().Context(),
		identity,
		session,
	)
	if apiError != nil {
		return apiError
	}
	items := make([]Document, 0, len(portForwards))
	for _, portForward := range portForwards {
		items = append(items, documentFromEntity(portForward))
	}
	_ = ctx.JSON(http.StatusOK, listDocument{Items: items})
	return nil
}

func (routes *Routes) pause(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	if apiError := routequery.RequireEmptyBody(ctx.Request()); apiError != nil {
		return apiError
	}
	portForward, apiError := routes.service.Pause(
		ctx.Request().Context(),
		identity,
		session,
		taskID,
	)
	if apiError != nil {
		return apiError
	}
	_ = ctx.JSON(http.StatusOK, documentFromEntity(portForward))
	return nil
}

func (routes *Routes) resume(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	if apiError := routequery.RequireEmptyBody(ctx.Request()); apiError != nil {
		return apiError
	}
	portForward, apiError := routes.service.Resume(
		ctx.Request().Context(), identity, session, taskID,
	)
	if apiError != nil {
		return apiError
	}
	_ = ctx.JSON(http.StatusOK, documentFromEntity(portForward))
	return nil
}

func (routes *Routes) delete(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	if apiError := routequery.RequireEmptyBody(ctx.Request()); apiError != nil {
		return apiError
	}
	portForward, apiError := routes.service.Delete(
		ctx.Request().Context(), identity, session, taskID,
	)
	if apiError != nil {
		return apiError
	}
	_ = ctx.JSON(http.StatusOK, documentFromEntity(portForward))
	return nil
}

func documentFromEntity(portForward portforwardservice.PortForward) Document {
	return Document{
		ID: portForward.ID, SessionID: portForward.SessionID, Namespace: portForward.Namespace,
		State: portForward.State, Kind: portForward.Kind, Name: portForward.Name,
		Protocol: portForward.Protocol, RemotePort: portForward.RemotePort,
		LocalPort:   portForward.LocalPort,
		DialAddress: portForward.DialAddress, CreatedAt: portForward.CreatedAt,
		UpdatedAt: portForward.UpdatedAt, ExpiresAt: portForward.ExpiresAt,
	}
}

func namespaceFromQuery(
	request *http.Request,
) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "namespace" || len(values) != 1 {
			return "", &controlplaneapi.Error{
				Code:    controlplaneapi.CodeInvalidArgument,
				Field:   key,
				Message: "only one namespace query parameter is supported",
			}
		}
	}
	namespace := query.Get("namespace")
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return "", &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "namespace",
			Message: "namespace is invalid",
		}
	}
	return namespace, nil
}
