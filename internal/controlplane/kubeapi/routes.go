package kubeapi

import (
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/labstack/echo/v5"
	kubernetesclient "k8s.io/client-go/kubernetes"
)

type Routes struct{ *Service }

func NewRoutes(service *Service) *Routes { return &Routes{Service: service} }

func (handler *Routes) Endpoints() controlplane.KubernetesEndpoints {
	return controlplane.KubernetesEndpoints{
		Version:      handler.withClient(handler.routeVersion),
		Capabilities: handler.withClient(handler.routeCapabilities),
		Namespaces:   handler.withClient(handler.routeNamespaces),
		Namespace:    handler.withClient(handler.routeNamespace),
		Pods:         handler.withClient(handler.routePods),
		Pod:          handler.withClient(handler.routePod),
		Services:     handler.withClient(handler.routeServices),
		Service:      handler.withClient(handler.routeService),
	}
}

type clientHandler func(*echo.Context, controlplaneapi.Identity, kubernetesclient.Interface) *controlplaneapi.Error

func (handler *Routes) withClient(next clientHandler) controlplane.EndpointFunc {
	return func(ctx *echo.Context, identity controlplaneapi.Identity) *controlplaneapi.Error {
		client, err := handler.provider.ClientFor(authorization.Subject{ID: identity.Subject, Groups: append([]string(nil), identity.Groups...)})
		if err != nil {
			return &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Kubernetes API is unavailable", Cause: err}
		}
		return next(ctx, identity, client)
	}
}

func (handler *Routes) routeVersion(ctx *echo.Context, _ controlplaneapi.Identity, client kubernetesclient.Interface) *controlplaneapi.Error {
	request := ctx.Request()
	if apiError := rejectQuery(request); apiError != nil {
		return apiError
	}
	return handler.version(ctx, client)
}

func (handler *Routes) routeCapabilities(ctx *echo.Context, identity controlplaneapi.Identity, client kubernetesclient.Interface) *controlplaneapi.Error {
	request := ctx.Request()
	namespace, apiError := capabilityNamespace(request)
	if apiError != nil {
		return apiError
	}
	return handler.capabilities(ctx, client, identity, namespace)
}

func (handler *Routes) routeNamespaces(ctx *echo.Context, identity controlplaneapi.Identity, client kubernetesclient.Interface) *controlplaneapi.Error {
	return handler.namespaces(ctx, client, identity)
}

func (handler *Routes) routeNamespace(ctx *echo.Context, _ controlplaneapi.Identity, client kubernetesclient.Interface) *controlplaneapi.Error {
	request := ctx.Request()
	namespace := ctx.Request().PathValue("namespace")
	if apiError := validateName("namespace", namespace, true); apiError != nil {
		return apiError
	}
	if apiError := rejectQuery(request); apiError != nil {
		return apiError
	}
	return handler.namespace(ctx, client, namespace)
}

func (handler *Routes) routePods(ctx *echo.Context, identity controlplaneapi.Identity, client kubernetesclient.Interface) *controlplaneapi.Error {
	return handler.routeInventory(ctx, identity, client, inventoryPods)
}

func (handler *Routes) routeServices(ctx *echo.Context, identity controlplaneapi.Identity, client kubernetesclient.Interface) *controlplaneapi.Error {
	return handler.routeInventory(ctx, identity, client, inventoryServices)
}

func (handler *Routes) routeInventory(ctx *echo.Context, identity controlplaneapi.Identity, client kubernetesclient.Interface, kind inventoryResource) *controlplaneapi.Error {
	request := ctx.Request()
	namespace := ctx.Request().PathValue("namespace")
	if apiError := validateName("namespace", namespace, true); apiError != nil {
		return apiError
	}
	if request.URL.Query().Get("watch") == "true" {
		return handler.watchInventory(ctx.Response(), request, client, identity, namespace, kind)
	}
	if kind == inventoryPods {
		return handler.pods(ctx, client, namespace)
	}
	return handler.services(ctx, client, namespace)
}

func (handler *Routes) routePod(ctx *echo.Context, _ controlplaneapi.Identity, client kubernetesclient.Interface) *controlplaneapi.Error {
	return handler.routeObject(ctx, client, inventoryPods)
}

func (handler *Routes) routeService(ctx *echo.Context, _ controlplaneapi.Identity, client kubernetesclient.Interface) *controlplaneapi.Error {
	return handler.routeObject(ctx, client, inventoryServices)
}

func (handler *Routes) routeObject(ctx *echo.Context, client kubernetesclient.Interface, kind inventoryResource) *controlplaneapi.Error {
	request := ctx.Request()
	namespace, name := ctx.Request().PathValue("namespace"), ctx.Request().PathValue("name")
	if apiError := validateNames(namespace, name); apiError != nil {
		return apiError
	}
	if apiError := rejectQuery(request); apiError != nil {
		return apiError
	}
	if kind == inventoryPods {
		return handler.pod(ctx, client, namespace, name)
	}
	return handler.service(ctx, client, namespace, name)
}
