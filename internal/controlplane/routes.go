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
