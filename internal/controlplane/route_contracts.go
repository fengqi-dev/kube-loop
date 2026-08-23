package controlplane

import (
	"github.com/labstack/echo/v5"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

// RouteRegistrar owns the HTTP routes for one API module.
type RouteRegistrar interface {
	RegisterRoutes(*echo.Group)
}

type SessionRouteRegistrar interface {
	RegisterSessionRoutes(*echo.Group)
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

type EndpointFunc func(*echo.Context, controlplaneapi.Identity) *controlplaneapi.Error
