package controlplane

import (
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/health"
	controlplanemiddleware "github.com/fengqi-dev/kube-loop/internal/controlplane/middleware"
	"github.com/labstack/echo/v5"
)

const (
	APIPathPrefix        = "/kubeloop/api"
	SessionAPIPathPrefix = "/api"
	AdminAPIPathPrefix   = "/api/admin"
)

// RouteRegistrar owns the HTTP routes for one API module.
type RouteRegistrar interface {
	RegisterRoutes(*echo.Group)
}

type SessionRouteRegistrar interface {
	RegisterSessionRoutes(*echo.Group)
}

func registerHealthRoutes(router *echo.Echo, handler *health.Handler) {
	router.GET("/health/live", handler.Live)
	router.GET("/health/ready", handler.Ready)
}

type RouteRegistrarFunc func(*echo.Group)

func (function RouteRegistrarFunc) RegisterRoutes(group *echo.Group) { function(group) }

type TicketEndpoints struct {
	Issue EndpointFunc
}

type PortForwardEndpoints struct {
	Create EndpointFunc
	List   EndpointFunc
	Stop   EndpointFunc
}

type RemoteTaskEndpoints struct {
	Create EndpointFunc
	Get    EndpointFunc
	Stop   EndpointFunc
}

type FileOperationEndpoints struct {
	List      EndpointFunc
	Create    EndpointFunc
	Rename    EndpointFunc
	Delete    EndpointFunc
	Operation EndpointFunc
}

type FileTransferEndpoints struct {
	Create EndpointFunc
	Get    EndpointFunc
	Stream EndpointFunc
}

type ExecEndpoints struct {
	Create EndpointFunc
	Stream EndpointFunc
}

type SessionEndpoints struct {
	Create     EndpointFunc
	Get        EndpointFunc
	Heartbeat  EndpointFunc
	Disconnect EndpointFunc
}

type KubernetesEndpoints struct {
	Version      EndpointFunc
	Capabilities EndpointFunc
	Namespaces   EndpointFunc
	Namespace    EndpointFunc
	Pods         EndpointFunc
	Pod          EndpointFunc
	Services     EndpointFunc
	Service      EndpointFunc
}

// APIRoutes is the complete, explicit Control Plane API routing table. Feature packages
// provide handlers, but they do not own HTTP methods or paths.
type APIRoutes struct {
	Tickets        TicketEndpoints
	PortForwards   PortForwardEndpoints
	Exchanges      RemoteTaskEndpoints
	Mirrors        RemoteTaskEndpoints
	Previews       RemoteTaskEndpoints
	FileOperations FileOperationEndpoints
	FileTransfers  FileTransferEndpoints
	Exec           ExecEndpoints
	Sessions       SessionEndpoints
	Kubernetes     KubernetesEndpoints
}

func (routes APIRoutes) RegisterRoutes(group *echo.Group) {
	registerRoute(group, http.MethodGet, "/version", routes.Kubernetes.Version)
	registerRoute(group, http.MethodGet, "/capabilities", routes.Kubernetes.Capabilities)
	registerRoute(group, http.MethodGet, "/namespaces", routes.Kubernetes.Namespaces)
	registerRoute(group, http.MethodGet, "/namespaces/:namespace", routes.Kubernetes.Namespace)
	registerRoute(group, http.MethodGet, "/namespaces/:namespace/pods", routes.Kubernetes.Pods)
	registerRoute(group, http.MethodGet, "/namespaces/:namespace/pods/:name", routes.Kubernetes.Pod)
	registerRoute(group, http.MethodGet, "/namespaces/:namespace/services", routes.Kubernetes.Services)
	registerRoute(group, http.MethodGet, "/namespaces/:namespace/services/:name", routes.Kubernetes.Service)
}

// RegisterSessionRoutes registers Session lifecycle and task resources under
// /api/sessions, independently from the general /kubeloop/api resource API.
func (routes APIRoutes) RegisterSessionRoutes(group *echo.Group) {
	registerRoute(group, http.MethodPost, "/sessions/:sessionID/tickets", routes.Tickets.Issue)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/port-forwards", routes.PortForwards.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/port-forwards", routes.PortForwards.List)
	registerRoute(group, http.MethodDelete, "/sessions/:sessionID/port-forwards/:taskID", routes.PortForwards.Stop)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/exchanges", routes.Exchanges.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/exchanges/:taskID", routes.Exchanges.Get)
	registerRoute(group, http.MethodDelete, "/sessions/:sessionID/exchanges/:taskID", routes.Exchanges.Stop)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/mirrors", routes.Mirrors.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/mirrors/:taskID", routes.Mirrors.Get)
	registerRoute(group, http.MethodDelete, "/sessions/:sessionID/mirrors/:taskID", routes.Mirrors.Stop)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/previews", routes.Previews.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/previews/:taskID", routes.Previews.Get)
	registerRoute(group, http.MethodDelete, "/sessions/:sessionID/previews/:taskID", routes.Previews.Stop)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/pod-files/list", routes.FileOperations.List)
	registerRoute(group, http.MethodPost, "/sessions/:sessionID/pod-files/create", routes.FileOperations.Create)
	registerRoute(group, http.MethodPost, "/sessions/:sessionID/pod-files/rename", routes.FileOperations.Rename)
	registerRoute(group, http.MethodPost, "/sessions/:sessionID/pod-files/delete", routes.FileOperations.Delete)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/pod-files/operations/:taskID", routes.FileOperations.Operation)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/file-transfers", routes.FileTransfers.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/file-transfers/:taskID", routes.FileTransfers.Get)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/file-transfers/:taskID/stream", routes.FileTransfers.Stream)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/exec", routes.Exec.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/exec/:taskID/stream", routes.Exec.Stream)

	registerRoute(group, http.MethodPost, "/sessions", routes.Sessions.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID", routes.Sessions.Get)
	registerRoute(group, http.MethodPost, "/sessions/:sessionID/heartbeat", routes.Sessions.Heartbeat)
	registerRoute(group, http.MethodDelete, "/sessions/:sessionID", routes.Sessions.Disconnect)
}

func registerRoute(group *echo.Group, method, path string, endpoint EndpointFunc) {
	if endpoint != nil {
		group.Add(method, path, Endpoint(endpoint))
	}
}

type EndpointFunc func(*echo.Context, controlplaneapi.Principal) *controlplaneapi.Error

// Endpoint adapts an API endpoint to Echo and copies Echo v5 path values to the
// standard request so transport-independent services can use Request.PathValue.
func Endpoint(function EndpointFunc) echo.HandlerFunc {
	return func(ctx *echo.Context) error {
		request := ctx.Request()
		for _, value := range ctx.PathValues() {
			request.SetPathValue(value.Name, value.Value)
		}
		principal, ok := controlplanemiddleware.PrincipalFromContext(request.Context())
		if !ok {
			return &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required"}
		}
		if apiError := function(ctx, principal); apiError != nil {
			return apiError
		}
		return nil
	}
}
