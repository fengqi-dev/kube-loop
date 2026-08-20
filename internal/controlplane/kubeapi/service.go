package kubeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
	"github.com/labstack/echo/v5"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/version"
	kubernetesclient "k8s.io/client-go/kubernetes"
)

const (
	defaultListLimit = int64(200)
	maximumListLimit = int64(500)
	maximumContinue  = 2048
)

type ClientProvider interface {
	ClientFor(authorization.Subject) (kubernetesclient.Interface, error)
}

type Service struct {
	provider        ClientProvider
	gatewayVersion  string
	inventory       *inventoryWatchHub
	inventoryResync time.Duration
}

type Option func(*Service)

func WithGatewayVersion(version string) Option {
	return func(handler *Service) { handler.gatewayVersion = strings.TrimSpace(version) }
}

func WithInventoryResync(interval time.Duration) Option {
	return func(handler *Service) { handler.inventoryResync = interval }
}

func New(provider ClientProvider, options ...Option) (*Service, error) {
	if provider == nil {
		return nil, errors.New("Kubernetes client Provider is required")
	}
	handler := &Service{
		provider: provider, gatewayVersion: "dev",
		inventoryResync: defaultInventoryResync,
	}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	if handler.inventoryResync <= 0 {
		handler.inventoryResync = defaultInventoryResync
	}
	handler.inventory = newInventoryWatchHub(handler.inventoryResync)
	return handler, nil
}

func (handler *Service) capabilities(
	ctx *echo.Context,
	client kubernetesclient.Interface,
	identity controlplaneapi.Identity,
	namespace string,
) *controlplaneapi.Error {
	request := ctx.Request()
	snapshot, apiError := handler.discoverCapabilities(request.Context(), client, identity, namespace)
	if apiError != nil {
		return apiError
	}
	writeJSON(ctx, snapshot)
	return nil
}

// DiscoverCapabilities reports the Kubernetes capabilities of the signed-in identity.
func (handler *Service) DiscoverCapabilities(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
) (capability.Snapshot, *controlplaneapi.Error) {
	client, err := handler.provider.ClientFor(authorization.Subject{
		ID: identity.Subject, Provider: identity.Provider, Groups: append([]string(nil), identity.Groups...),
	})
	if err != nil {
		return capability.Snapshot{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeUnavailable, Message: "Kubernetes API is unavailable", Cause: err,
		}
	}
	return handler.discoverCapabilities(ctx, client, identity, namespace)
}

func (handler *Service) discoverCapabilities(
	ctx context.Context,
	client kubernetesclient.Interface,
	identity controlplaneapi.Identity,
	namespace string,
) (capability.Snapshot, *controlplaneapi.Error) {
	type candidate struct {
		capability string
		kubernetes []authorizationv1.ResourceAttributes
	}
	namespaced := func(attributes ...authorizationv1.ResourceAttributes) []authorizationv1.ResourceAttributes {
		for index := range attributes {
			attributes[index].Namespace = namespace
		}
		return attributes
	}
	candidates := []candidate{
		{capability: "pods.get", kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "get", Resource: "pods"})},
		{capability: "pods.list", kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "list", Resource: "pods"})},
		{capability: "pods.watch", kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "watch", Resource: "pods"})},
		{capability: "services.get", kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "get", Resource: "services"})},
		{capability: "services.list", kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "list", Resource: "services"})},
		{capability: "services.watch", kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "watch", Resource: "services"})},
		{capability: "cluster.tunnel", kubernetes: namespaced(
			authorizationv1.ResourceAttributes{Verb: "list", Resource: "pods"},
			authorizationv1.ResourceAttributes{Verb: "list", Resource: "services"},
		)},
		{capability: "ports.forward", kubernetes: namespaced(
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "pods"},
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "services"},
		)},
		{capability: "pods.exec", kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "create", Resource: "pods", Subresource: "exec"})},
		{capability: "pods.files", kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "create", Resource: "pods", Subresource: "exec"})},
		{capability: "pods.files.manage", kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "create", Resource: "pods", Subresource: "exec"})},
		{capability: "services.exchange", kubernetes: namespaced(
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "services"},
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "endpoints"},
			authorizationv1.ResourceAttributes{Group: "discovery.k8s.io", Verb: "list", Resource: "endpointslices"},
		)},
		{capability: "services.mirror", kubernetes: namespaced(
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "services"},
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "endpoints"},
			authorizationv1.ResourceAttributes{Group: "discovery.k8s.io", Verb: "list", Resource: "endpointslices"},
		)},
		{capability: "services.preview"},
	}
	capabilities := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		kubernetesAllowed := true
		for _, attributes := range candidate.kubernetes {
			review, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authorizationv1.SelfSubjectAccessReview{
				Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &attributes},
			}, metav1.CreateOptions{})
			if err != nil {
				return capability.Snapshot{}, mapKubernetesError(err)
			}
			if !review.Status.Allowed {
				kubernetesAllowed = false
				break
			}
		}
		if kubernetesAllowed {
			capabilities = append(capabilities, candidate.capability)
		}
	}
	snapshot, err := capability.Normalize(capability.Snapshot{
		SchemaVersion: capability.SchemaVersion, IdentityID: identity.Subject, Namespace: namespace,
		GatewayVersion: handler.gatewayVersion, Capabilities: capabilities,
	})
	if err != nil {
		return capability.Snapshot{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeInternal, Message: "capability snapshot validation failed", Cause: err,
		}
	}
	return snapshot, nil
}

func (handler *Service) version(ctx *echo.Context, client kubernetesclient.Interface) *controlplaneapi.Error {
	request := ctx.Request()
	var result version.Info
	contents, err := client.Discovery().RESTClient().Get().AbsPath("/version").Do(request.Context()).Raw()
	if err != nil {
		return mapKubernetesError(err)
	}
	if err := json.Unmarshal(contents, &result); err != nil {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "Kubernetes operation failed", Cause: err}
	}
	writeJSON(ctx, versionDocument{GitVersion: result.GitVersion, GatewayVersion: handler.gatewayVersion})
	return nil
}

func (handler *Service) namespaces(
	ctx *echo.Context,
	client kubernetesclient.Interface,
	identity controlplaneapi.Identity,
) *controlplaneapi.Error {
	request := ctx.Request()
	options, apiError := listOptions(request)
	if apiError != nil {
		return apiError
	}
	result, listErr := client.CoreV1().Namespaces().List(request.Context(), options)
	if listErr != nil {
		return mapKubernetesError(listErr)
	}
	items := make([]namespaceDocument, 0, len(result.Items))
	for index := range result.Items {
		items = append(items, namespaceDocument{Name: result.Items[index].Name, Status: string(result.Items[index].Status.Phase)})
	}
	writeJSON(ctx, listDocument[namespaceDocument]{Items: items, Continue: result.Continue, ResourceVersion: result.ResourceVersion})
	return nil
}

func (handler *Service) namespace(ctx *echo.Context, client kubernetesclient.Interface, name string) *controlplaneapi.Error {
	request := ctx.Request()
	result, err := client.CoreV1().Namespaces().Get(request.Context(), name, metav1.GetOptions{})
	if err != nil {
		return mapKubernetesError(err)
	}
	writeJSON(ctx, namespaceDocument{Name: result.Name, Status: string(result.Status.Phase)})
	return nil
}

func (handler *Service) pods(ctx *echo.Context, client kubernetesclient.Interface, namespace string) *controlplaneapi.Error {
	request := ctx.Request()
	options, apiError := listOptions(request)
	if apiError != nil {
		return apiError
	}
	result, err := client.CoreV1().Pods(namespace).List(request.Context(), options)
	if err != nil {
		return mapKubernetesError(err)
	}
	items := make([]podDocument, 0, len(result.Items))
	for index := range result.Items {
		items = append(items, podFromKubernetes(&result.Items[index]))
	}
	writeJSON(ctx, listDocument[podDocument]{Items: items, Continue: result.Continue, ResourceVersion: result.ResourceVersion})
	return nil
}

func (handler *Service) pod(ctx *echo.Context, client kubernetesclient.Interface, namespace, name string) *controlplaneapi.Error {
	request := ctx.Request()
	result, err := client.CoreV1().Pods(namespace).Get(request.Context(), name, metav1.GetOptions{})
	if err != nil {
		return mapKubernetesError(err)
	}
	writeJSON(ctx, podFromKubernetes(result))
	return nil
}

func (handler *Service) services(ctx *echo.Context, client kubernetesclient.Interface, namespace string) *controlplaneapi.Error {
	request := ctx.Request()
	options, apiError := listOptions(request)
	if apiError != nil {
		return apiError
	}
	result, err := client.CoreV1().Services(namespace).List(request.Context(), options)
	if err != nil {
		return mapKubernetesError(err)
	}
	items := make([]serviceDocument, 0, len(result.Items))
	for index := range result.Items {
		items = append(items, serviceFromKubernetes(&result.Items[index]))
	}
	writeJSON(ctx, listDocument[serviceDocument]{Items: items, Continue: result.Continue, ResourceVersion: result.ResourceVersion})
	return nil
}

func (handler *Service) service(ctx *echo.Context, client kubernetesclient.Interface, namespace, name string) *controlplaneapi.Error {
	request := ctx.Request()
	result, err := client.CoreV1().Services(namespace).Get(request.Context(), name, metav1.GetOptions{})
	if err != nil {
		return mapKubernetesError(err)
	}
	writeJSON(ctx, serviceFromKubernetes(result))
	return nil
}

func listOptions(request *http.Request) (metav1.ListOptions, *controlplaneapi.Error) {
	for key, values := range request.URL.Query() {
		if key != "limit" && key != "continue" && key != "labelSelector" && key != "fieldSelector" {
			return metav1.ListOptions{}, invalidQuery(key)
		}
		if len(values) != 1 {
			return metav1.ListOptions{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeInvalidArgument, Field: key, Message: "query parameter must be provided once",
			}
		}
	}
	limit := defaultListLimit
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 || parsed > maximumListLimit {
			return metav1.ListOptions{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeInvalidArgument, Field: "limit",
				Message: fmt.Sprintf("limit must be between 1 and %d", maximumListLimit),
			}
		}
		limit = parsed
	}
	continueToken := request.URL.Query().Get("continue")
	if len(continueToken) > maximumContinue || containsControl(continueToken) {
		return metav1.ListOptions{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeInvalidArgument, Field: "continue", Message: "continue token is invalid",
		}
	}
	labelSelector := request.URL.Query().Get("labelSelector")
	if len(labelSelector) > 1024 || containsControl(labelSelector) {
		return metav1.ListOptions{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeInvalidArgument, Field: "labelSelector", Message: "label selector is invalid",
		}
	}
	if labelSelector != "" {
		if _, err := labels.Parse(labelSelector); err != nil {
			return metav1.ListOptions{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeInvalidArgument, Field: "labelSelector", Message: "label selector is invalid",
			}
		}
	}
	fieldSelector := request.URL.Query().Get("fieldSelector")
	if len(fieldSelector) > 1024 || containsControl(fieldSelector) {
		return metav1.ListOptions{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeInvalidArgument, Field: "fieldSelector", Message: "field selector is invalid",
		}
	}
	if fieldSelector != "" {
		if _, err := fields.ParseSelector(fieldSelector); err != nil {
			return metav1.ListOptions{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeInvalidArgument, Field: "fieldSelector", Message: "field selector is invalid",
			}
		}
	}
	return metav1.ListOptions{
		Limit: limit, Continue: continueToken, LabelSelector: labelSelector, FieldSelector: fieldSelector,
	}, nil
}

func capabilityNamespace(request *http.Request) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "namespace" {
			return "", invalidQuery(key)
		}
		if len(values) != 1 {
			return "", &controlplaneapi.Error{
				Code: controlplaneapi.CodeInvalidArgument, Field: key, Message: "query parameter must be provided once",
			}
		}
	}
	namespace := query.Get("namespace")
	if namespace == "" {
		return "", &controlplaneapi.Error{
			Code: controlplaneapi.CodeInvalidArgument, Field: "namespace", Message: "namespace is required",
		}
	}
	if apiError := validateName("namespace", namespace, true); apiError != nil {
		return "", apiError
	}
	return namespace, nil
}

func rejectQuery(request *http.Request) *controlplaneapi.Error {
	for key := range request.URL.Query() {
		return invalidQuery(key)
	}
	return nil
}

func invalidQuery(field string) *controlplaneapi.Error {
	return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: field, Message: "query parameter is not supported"}
}

func validateNames(namespace, name string) *controlplaneapi.Error {
	if apiError := validateName("namespace", namespace, true); apiError != nil {
		return apiError
	}
	return validateName("name", name, false)
}

func validateName(field, value string, namespace bool) *controlplaneapi.Error {
	var problems []string
	if namespace {
		problems = validation.IsDNS1123Label(value)
	} else {
		problems = validation.IsDNS1123Subdomain(value)
	}
	if len(problems) != 0 {
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInvalidArgument, Field: field, Message: field + " is invalid"}
	}
	return nil
}

func podFromKubernetes(pod *corev1.Pod) podDocument {
	containers := make([]string, 0, len(pod.Spec.Containers))
	ports := make([]podPortDocument, 0)
	for _, container := range pod.Spec.Containers {
		containers = append(containers, container.Name)
		for _, port := range container.Ports {
			ports = append(ports, podPortDocument{
				Name: port.Name, Port: port.ContainerPort, Protocol: string(port.Protocol),
			})
		}
	}
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			ready = true
			break
		}
	}
	var readyContainers, restarts int32
	for _, status := range pod.Status.ContainerStatuses {
		if status.Ready {
			readyContainers++
		}
		restarts += status.RestartCount
	}
	return podDocument{
		Name: pod.Name, Namespace: pod.Namespace, Phase: string(pod.Status.Phase),
		PodIP: pod.Status.PodIP, NodeName: pod.Spec.NodeName, Ready: ready,
		ReadyContainers: readyContainers, TotalContainers: int32(len(pod.Spec.Containers)), Restarts: restarts,
		AgeSeconds: resourceAgeSeconds(pod.CreationTimestamp), Containers: containers, Ports: ports,
	}
}

func serviceFromKubernetes(service *corev1.Service) serviceDocument {
	ports := make([]servicePortDocument, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		ports = append(ports, servicePortDocument{
			Name: port.Name, Port: port.Port, Protocol: string(port.Protocol), TargetPort: port.TargetPort.String(),
		})
	}
	externalIPs := append([]string(nil), service.Spec.ExternalIPs...)
	for _, ingress := range service.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			externalIPs = append(externalIPs, ingress.IP)
		} else if ingress.Hostname != "" {
			externalIPs = append(externalIPs, ingress.Hostname)
		}
	}
	if service.Spec.ExternalName != "" {
		externalIPs = append(externalIPs, service.Spec.ExternalName)
	}
	return serviceDocument{
		Name: service.Name, Namespace: service.Namespace, Type: string(service.Spec.Type),
		ClusterIP: service.Spec.ClusterIP, ExternalName: service.Spec.ExternalName,
		ExternalIPs: externalIPs, AgeSeconds: resourceAgeSeconds(service.CreationTimestamp), Ports: ports,
	}
}

func resourceAgeSeconds(created metav1.Time) int64 {
	if created.Time.IsZero() {
		return 0
	}
	age := int64(time.Since(created.Time).Seconds())
	if age < 0 {
		return 0
	}
	return age
}

func mapKubernetesError(err error) *controlplaneapi.Error {
	switch {
	case apierrors.IsNotFound(err):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found", Cause: err}
	case apierrors.IsForbidden(err):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeForbidden, Message: "Kubernetes operation is not permitted", Cause: err}
	case apierrors.IsTooManyRequests(err):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeRateLimited, Message: "Kubernetes API rate limit exceeded", Cause: err}
	case apierrors.IsUnauthorized(err) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsServiceUnavailable(err) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return &controlplaneapi.Error{Code: controlplaneapi.CodeUnavailable, Message: "Kubernetes API is unavailable", Cause: err}
	default:
		return &controlplaneapi.Error{Code: controlplaneapi.CodeInternal, Message: "Kubernetes operation failed", Cause: err}
	}
}

func writeJSON(ctx *echo.Context, value any) {
	_ = ctx.JSON(http.StatusOK, value)
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
