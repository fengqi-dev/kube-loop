package portforwardapi

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/taskapi"
	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"
)

type SessionValidator interface {
	RequireActive(context.Context, controlplaneapi.Identity, string, string) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type Routes struct {
	service  *portforwardservice.Service
	sessions SessionValidator
}

func NewRoutes(service *portforwardservice.Service, sessions SessionValidator) *Routes {
	return &Routes{service: service, sessions: sessions}
}

func (routes *Routes) Endpoints() controlplane.PortForwardEndpoints {
	return controlplane.PortForwardEndpoints{
		Create: routes.withSession(routes.create),
		List:   routes.withSession(routes.list),
		Stop:   routes.withTask(routes.stop),
	}
}

type sessionHandler func(*echo.Context, controlplaneapi.Identity, sessionapi.ActiveSession) *controlplaneapi.Error
type taskHandler func(*echo.Context, controlplaneapi.Identity, sessionapi.ActiveSession, string) *controlplaneapi.Error

func (routes *Routes) withSession(next sessionHandler) controlplane.EndpointFunc {
	return func(ctx *echo.Context, identity controlplaneapi.Identity) *controlplaneapi.Error {
		active, apiError := routes.activeSession(ctx.Request(), identity)
		if apiError != nil {
			return apiError
		}
		return next(ctx, identity, active)
	}
}

func (routes *Routes) withTask(next taskHandler) controlplane.EndpointFunc {
	return routes.withSession(func(ctx *echo.Context, identity controlplaneapi.Identity, active sessionapi.ActiveSession) *controlplaneapi.Error {
		return next(ctx, identity, active, ctx.Request().PathValue("taskID"))
	})
}

func (routes *Routes) activeSession(request *http.Request, identity controlplaneapi.Identity) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	namespace, apiError := namespaceFromQuery(request)
	if apiError != nil {
		return sessionapi.ActiveSession{}, apiError
	}
	active, apiError := routes.sessions.RequireActive(request.Context(), identity, namespace, request.PathValue("sessionID"))
	if apiError != nil {
		return sessionapi.ActiveSession{}, apiError
	}
	controlplanemiddleware.SetAuditSessionID(request.Context(), active.ID)
	return active, nil
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
		controlplane.APIPathPrefix, session.ID, result.PortForward.ID, session.Namespace,
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
	if apiError := requireEmptyBody(ctx.Request()); apiError != nil {
		return apiError
	}
	portForwards, apiError := routes.service.List(ctx.Request().Context(), identity, session)
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

func (routes *Routes) stop(
	ctx *echo.Context,
	identity controlplaneapi.Identity,
	session sessionapi.ActiveSession,
	taskID string,
) *controlplaneapi.Error {
	if apiError := requireEmptyBody(ctx.Request()); apiError != nil {
		return apiError
	}
	portForward, apiError := routes.service.Stop(ctx.Request().Context(), identity, session, taskID)
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
		DialAddress: portForward.DialAddress, CreatedAt: portForward.CreatedAt,
		UpdatedAt: portForward.UpdatedAt, ExpiresAt: portForward.ExpiresAt,
	}
}

func namespaceFromQuery(request *http.Request) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "namespace" || len(values) != 1 {
			return "", &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: key, Message: "only one namespace query parameter is supported"}
		}
	}
	namespace := query.Get("namespace")
	if len(validation.IsDNS1123Label(namespace)) != 0 {
		return "", &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: "namespace", Message: "namespace is invalid"}
	}
	return namespace, nil
}

func requireEmptyBody(request *http.Request) *controlplaneapi.Error {
	contents, err := io.ReadAll(io.LimitReader(request.Body, 1))
	if err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Message: "request body is invalid"}
	}
	if len(contents) != 0 {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Message: "request body must be empty"}
	}
	return nil
}
