package sessionroute

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
)

type Sessions interface {
	RequireActive(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
	) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

type Handler func(*echo.Context, controlplaneapi.Identity, sessionapi.ActiveSession) *controlplaneapi.Error

type TaskHandler func(
	*echo.Context,
	controlplaneapi.Identity,
	sessionapi.ActiveSession,
	string,
) *controlplaneapi.Error

type NamespaceResolver func(*http.Request) (string, *controlplaneapi.Error)

func WithSession(sessions Sessions, next Handler) controlplane.EndpointFunc {
	return WithSessionResolver(sessions, NamespaceFromQuery, next)
}

func WithSessionResolver(
	sessions Sessions,
	resolveNamespace NamespaceResolver,
	next Handler,
) controlplane.EndpointFunc {
	return func(ctx *echo.Context, identity controlplaneapi.Identity) *controlplaneapi.Error {
		request := ctx.Request()
		session, apiError := ActiveSessionWithResolver(request, identity, sessions, resolveNamespace)
		if apiError != nil {
			return apiError
		}
		return next(ctx, identity, session)
	}
}

func WithTask(sessions Sessions, next TaskHandler) controlplane.EndpointFunc {
	return WithTaskResolver(sessions, NamespaceFromQuery, next)
}

func WithTaskResolver(
	sessions Sessions,
	resolveNamespace NamespaceResolver,
	next TaskHandler,
) controlplane.EndpointFunc {
	return WithSessionResolver(
		sessions,
		resolveNamespace,
		func(ctx *echo.Context, identity controlplaneapi.Identity, session sessionapi.ActiveSession) *controlplaneapi.Error {
			return next(ctx, identity, session, ctx.Request().PathValue("taskID"))
		},
	)
}

func ActiveSession(
	request *http.Request,
	identity controlplaneapi.Identity,
	sessions Sessions,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	return ActiveSessionWithResolver(request, identity, sessions, NamespaceFromQuery)
}

func ActiveSessionWithResolver(
	request *http.Request,
	identity controlplaneapi.Identity,
	sessions Sessions,
	resolveNamespace NamespaceResolver,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	namespace, apiError := resolveNamespace(request)
	if apiError != nil {
		return sessionapi.ActiveSession{}, apiError
	}
	session, apiError := sessions.RequireActive(
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

func NamespaceFromQuery(request *http.Request) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["namespace"]) != 1 ||
		len(validation.IsDNS1123Label(query.Get("namespace"))) != 0 {
		return "", &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "namespace",
			Message: "one valid namespace query parameter is required",
		}
	}
	return query.Get("namespace"), nil
}
