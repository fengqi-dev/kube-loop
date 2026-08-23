package controlplane

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/health"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
)

const (
	APIPathPrefix   = "/api"
	AdminPathPrefix = "/admin"
)

func registerHealthRoutes(router *echo.Echo, handler *health.Handler) {
	router.GET("/health/live", handler.Live)
	router.GET("/health/ready", handler.Ready)
}

func (routes APIRoutes) RegisterRoutes(group *echo.Group) {
	registerRoute(group, http.MethodGet, "/version", routes.Kubernetes.Version)
	registerRoute(group, http.MethodGet, "/capabilities", routes.Kubernetes.Capabilities)
	registerRoute(group, http.MethodGet, "/namespaces", routes.Kubernetes.Namespaces)
	registerRoute(group, http.MethodGet, "/namespaces/:namespace", routes.Kubernetes.Namespace)
	registerRoute(group, http.MethodGet, "/namespaces/:namespace/pods", routes.Kubernetes.Pods)
	registerRoute(group, http.MethodGet, "/namespaces/:namespace/pods/:name", routes.Kubernetes.Pod)
	registerRoute(
		group,
		http.MethodGet,
		"/namespaces/:namespace/services",
		routes.Kubernetes.Services,
	)
	registerRoute(
		group,
		http.MethodGet,
		"/namespaces/:namespace/services/:name",
		routes.Kubernetes.Service,
	)
}

// RegisterSessionRoutes registers Session lifecycle and task resources under
// /api/sessions, alongside the general /api resource API.
func (routes APIRoutes) RegisterSessionRoutes(group *echo.Group) {
	registerRoute(group, http.MethodPost, "/sessions/:sessionID/tickets", routes.Tickets.Issue)

	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/port-forwards",
		routes.PortForwards.Create,
	)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/port-forwards",
		routes.PortForwards.List,
	)
	registerRoute(
		group,
		http.MethodDelete,
		"/sessions/:sessionID/port-forwards/:taskID",
		routes.PortForwards.Stop,
	)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/exchanges", routes.Exchanges.Create)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/exchanges/:taskID",
		routes.Exchanges.Get,
	)
	registerRoute(
		group,
		http.MethodDelete,
		"/sessions/:sessionID/exchanges/:taskID",
		routes.Exchanges.Stop,
	)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/mirrors", routes.Mirrors.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/mirrors/:taskID", routes.Mirrors.Get)
	registerRoute(
		group,
		http.MethodDelete,
		"/sessions/:sessionID/mirrors/:taskID",
		routes.Mirrors.Stop,
	)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/previews", routes.Previews.Create)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/previews/:taskID",
		routes.Previews.Get,
	)
	registerRoute(
		group,
		http.MethodDelete,
		"/sessions/:sessionID/previews/:taskID",
		routes.Previews.Stop,
	)

	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/pod-files/list",
		routes.FileOperations.List,
	)
	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/pod-files/create",
		routes.FileOperations.Create,
	)
	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/pod-files/rename",
		routes.FileOperations.Rename,
	)
	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/pod-files/delete",
		routes.FileOperations.Delete,
	)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/pod-files/operations/:taskID",
		routes.FileOperations.Operation,
	)

	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/file-transfers",
		routes.FileTransfers.Create,
	)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/file-transfers/:taskID",
		routes.FileTransfers.Get,
	)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/file-transfers/:taskID/stream",
		routes.FileTransfers.Stream,
	)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/exec", routes.Exec.Create)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/exec/:taskID/stream",
		routes.Exec.Stream,
	)

	registerRoute(group, http.MethodPost, "/sessions", routes.Sessions.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID", routes.Sessions.Get)
	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/heartbeat",
		routes.Sessions.Heartbeat,
	)
	registerRoute(group, http.MethodDelete, "/sessions/:sessionID", routes.Sessions.Disconnect)
}

func registerRoute(group *echo.Group, method, path string, endpoint EndpointFunc) {
	if endpoint != nil {
		group.Add(method, path, Endpoint(endpoint))
	}
}

// Endpoint adapts an API endpoint to Echo and copies Echo v5 path values to the
// standard request so transport-independent services can use Request.PathValue.
func Endpoint(function EndpointFunc) echo.HandlerFunc {
	return func(ctx *echo.Context) error {
		request := ctx.Request()
		for _, value := range ctx.PathValues() {
			request.SetPathValue(value.Name, value.Value)
		}
		identity, ok := controlplanemiddleware.IdentityFromContext(request.Context())
		if !ok {
			return &controlplaneapi.Error{
				Code:    controlplaneapi.CodeUnauthenticated,
				Message: "authentication required",
			}
		}
		if apiError := function(ctx, identity); apiError != nil {
			return apiError
		}
		return nil
	}
}
