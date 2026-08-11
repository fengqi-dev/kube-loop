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

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
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

type Handler struct {
	provider        ClientProvider
	authorizer      authorization.Authorizer
	gatewayVersion  string
	inventory       *inventoryWatchHub
	inventoryResync time.Duration
}

type Option func(*Handler)

func WithCapabilityAuthorizer(authorizer authorization.Authorizer) Option {
	return func(handler *Handler) { handler.authorizer = authorizer }
}

func WithGatewayVersion(version string) Option {
	return func(handler *Handler) { handler.gatewayVersion = strings.TrimSpace(version) }
}

func WithInventoryResync(interval time.Duration) Option {
	return func(handler *Handler) { handler.inventoryResync = interval }
}

func New(provider ClientProvider, options ...Option) (*Handler, error) {
	if provider == nil {
		return nil, errors.New("Kubernetes client Provider is required")
	}
	handler := &Handler{
		provider: provider, authorizer: authorization.NewDenyAll(), gatewayVersion: "dev",
		inventoryResync: defaultInventoryResync,
	}
	for _, option := range options {
		if option != nil {
			option(handler)
		}
	}
	if handler.authorizer == nil {
		return nil, errors.New("capability Authorizer is required")
	}
	if handler.inventoryResync <= 0 {
		handler.inventoryResync = defaultInventoryResync
	}
	handler.inventory = newInventoryWatchHub(handler.inventoryResync)
	return handler, nil
}

type listDocument[T any] struct {
	Items           []T    `json:"items"`
	Continue        string `json:"continue,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

type versionDocument struct {
	GitVersion     string `json:"gitVersion"`
	GatewayVersion string `json:"gatewayVersion"`
}

type namespaceDocument struct {
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

type podDocument struct {
	Name       string            `json:"name"`
	Namespace  string            `json:"namespace"`
	Phase      string            `json:"phase,omitempty"`
	PodIP      string            `json:"podIp,omitempty"`
	NodeName   string            `json:"nodeName,omitempty"`
	Ready      bool              `json:"ready"`
	Containers []string          `json:"containers"`
	Ports      []podPortDocument `json:"ports"`
}

type podPortDocument struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

type serviceDocument struct {
	Name         string                `json:"name"`
	Namespace    string                `json:"namespace"`
	Type         string                `json:"type"`
	ClusterIP    string                `json:"clusterIp,omitempty"`
	ExternalName string                `json:"externalName,omitempty"`
	Ports        []servicePortDocument `json:"ports"`
}

type servicePortDocument struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	Protocol   string `json:"protocol"`
	TargetPort string `json:"targetPort,omitempty"`
}

func (handler *Handler) ServeAPI(writer http.ResponseWriter, request *http.Request, principal controller.Principal) *controller.APIError {
	if request.Method != http.MethodGet {
		return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
	}
	client, err := handler.provider.ClientFor(authorization.Subject{
		ID: principal.Subject, Groups: append([]string(nil), principal.Groups...),
	})
	if err != nil {
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Kubernetes API is unavailable", Cause: err}
	}
	parts, validPath := routeParts(request.URL.Path)
	if !validPath {
		return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
	}
	switch {
	case len(parts) == 1 && parts[0] == "version":
		if apiError := rejectQuery(request); apiError != nil {
			return apiError
		}
		return handler.version(writer, request, client)
	case len(parts) == 1 && parts[0] == "capabilities":
		namespace, apiError := capabilityNamespace(request)
		if apiError != nil {
			return apiError
		}
		return handler.capabilities(writer, request, client, principal, namespace)
	case len(parts) == 1 && parts[0] == "namespaces":
		return handler.namespaces(writer, request, client, principal)
	case len(parts) == 2 && parts[0] == "namespaces":
		if apiError := validateName("namespace", parts[1], true); apiError != nil {
			return apiError
		}
		if apiError := rejectQuery(request); apiError != nil {
			return apiError
		}
		return handler.namespace(writer, request, client, parts[1])
	case len(parts) == 3 && parts[0] == "namespaces" && parts[2] == "pods":
		if apiError := validateName("namespace", parts[1], true); apiError != nil {
			return apiError
		}
		if request.URL.Query().Get("watch") == "true" {
			return handler.watchInventory(writer, request, client, principal, parts[1], inventoryPods)
		}
		return handler.pods(writer, request, client, parts[1])
	case len(parts) == 4 && parts[0] == "namespaces" && parts[2] == "pods":
		if apiError := validateNames(parts[1], parts[3]); apiError != nil {
			return apiError
		}
		if apiError := rejectQuery(request); apiError != nil {
			return apiError
		}
		return handler.pod(writer, request, client, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "namespaces" && parts[2] == "services":
		if apiError := validateName("namespace", parts[1], true); apiError != nil {
			return apiError
		}
		if request.URL.Query().Get("watch") == "true" {
			return handler.watchInventory(writer, request, client, principal, parts[1], inventoryServices)
		}
		return handler.services(writer, request, client, parts[1])
	case len(parts) == 4 && parts[0] == "namespaces" && parts[2] == "services":
		if apiError := validateNames(parts[1], parts[3]); apiError != nil {
			return apiError
		}
		if apiError := rejectQuery(request); apiError != nil {
			return apiError
		}
		return handler.service(writer, request, client, parts[1], parts[3])
	default:
		return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found"}
	}
}

func (handler *Handler) capabilities(
	writer http.ResponseWriter,
	request *http.Request,
	client kubernetesclient.Interface,
	principal controller.Principal,
	namespace string,
) *controller.APIError {
	snapshot, apiError := handler.discoverCapabilities(request.Context(), client, principal, namespace)
	if apiError != nil {
		return apiError
	}
	writeJSON(writer, snapshot)
	return nil
}

// DiscoverCapabilities returns the same policy/Kubernetes intersection used by
// GET /capabilities so Session creation cannot drift from the advertised API.
func (handler *Handler) DiscoverCapabilities(
	ctx context.Context,
	principal controller.Principal,
	namespace string,
) (capability.Snapshot, *controller.APIError) {
	client, err := handler.provider.ClientFor(authorization.Subject{
		ID: principal.Subject, Groups: append([]string(nil), principal.Groups...),
	})
	if err != nil {
		return capability.Snapshot{}, &controller.APIError{
			Code: controller.CodeUnavailable, Message: "Kubernetes API is unavailable", Cause: err,
		}
	}
	return handler.discoverCapabilities(ctx, client, principal, namespace)
}

func (handler *Handler) discoverCapabilities(
	ctx context.Context,
	client kubernetesclient.Interface,
	principal controller.Principal,
	namespace string,
) (capability.Snapshot, *controller.APIError) {
	type candidate struct {
		capability string
		policy     []authorization.Request
		kubernetes []authorizationv1.ResourceAttributes
	}
	policyRequests := func(resource string, operations ...string) []authorization.Request {
		result := make([]authorization.Request, 0, len(operations))
		for _, operation := range operations {
			result = append(result, authorization.Request{Operation: operation, Namespace: namespace, ResourceKind: resource})
		}
		return result
	}
	namespaced := func(attributes ...authorizationv1.ResourceAttributes) []authorizationv1.ResourceAttributes {
		for index := range attributes {
			attributes[index].Namespace = namespace
		}
		return attributes
	}
	candidates := []candidate{
		{capability: "pods.get", policy: policyRequests("pods", "get"), kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "get", Resource: "pods"})},
		{capability: "pods.list", policy: policyRequests("pods", "list"), kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "list", Resource: "pods"})},
		{capability: "pods.watch", policy: policyRequests("pods", "watch"), kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "watch", Resource: "pods"})},
		{capability: "services.get", policy: policyRequests("services", "get"), kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "get", Resource: "services"})},
		{capability: "services.list", policy: policyRequests("services", "list"), kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "list", Resource: "services"})},
		{capability: "services.watch", policy: policyRequests("services", "watch"), kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "watch", Resource: "services"})},
		{capability: "cluster.tunnel", policy: append(
			policyRequests("sessions", "create", "get", "heartbeat", "delete"),
			policyRequests("relay-tickets", "create")...,
		), kubernetes: namespaced(
			authorizationv1.ResourceAttributes{Verb: "list", Resource: "pods"},
			authorizationv1.ResourceAttributes{Verb: "list", Resource: "services"},
		)},
		{capability: "ports.forward", policy: policyRequests("port-forwards", "create", "list", "delete"), kubernetes: namespaced(
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "pods"},
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "services"},
		)},
		{capability: "pods.exec", policy: policyRequests("pod-exec", "create", "stream"), kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "create", Resource: "pods", Subresource: "exec"})},
		{capability: "pods.files", policy: policyRequests("file-transfers", "create", "stream"), kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "create", Resource: "pods", Subresource: "exec"})},
		{capability: "pods.files.manage", policy: policyRequests("pod-files", "list", "create", "update", "delete", "get"), kubernetes: namespaced(authorizationv1.ResourceAttributes{Verb: "create", Resource: "pods", Subresource: "exec"})},
		{capability: "services.exchange", policy: policyRequests("exchanges", "create", "get", "delete", "stream"), kubernetes: namespaced(
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "services"},
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "endpoints"},
			authorizationv1.ResourceAttributes{Group: "discovery.k8s.io", Verb: "list", Resource: "endpointslices"},
		)},
		{capability: "services.mirror", policy: policyRequests("mirrors", "create", "get", "delete", "stream"), kubernetes: namespaced(
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "services"},
			authorizationv1.ResourceAttributes{Verb: "get", Resource: "endpoints"},
			authorizationv1.ResourceAttributes{Group: "discovery.k8s.io", Verb: "list", Resource: "endpointslices"},
		)},
		// Preview Kubernetes objects are owned and mutated by the Operator. The
		// Controller only creates the TrafficBinding after the Gateway policy check.
		{capability: "services.preview", policy: policyRequests("previews", "create", "get", "delete", "stream")},
	}
	subject := authorization.Subject{ID: principal.Subject, Groups: append([]string(nil), principal.Groups...)}
	capabilities := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		policyAllowed := true
		for _, policyRequest := range candidate.policy {
			decision := handler.authorizer.Authorize(ctx, subject, policyRequest)
			if !decision.Allowed {
				policyAllowed = false
				break
			}
		}
		if !policyAllowed {
			continue
		}
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
		SchemaVersion: capability.SchemaVersion, PrincipalID: principal.Subject, Namespace: namespace,
		GatewayVersion: handler.gatewayVersion, Capabilities: capabilities,
	})
	if err != nil {
		return capability.Snapshot{}, &controller.APIError{
			Code: controller.CodeInternal, Message: "capability snapshot validation failed", Cause: err,
		}
	}
	return snapshot, nil
}

func (handler *Handler) version(writer http.ResponseWriter, request *http.Request, client kubernetesclient.Interface) *controller.APIError {
	var result version.Info
	contents, err := client.Discovery().RESTClient().Get().AbsPath("/version").Do(request.Context()).Raw()
	if err != nil {
		return mapKubernetesError(err)
	}
	if err := json.Unmarshal(contents, &result); err != nil {
		return &controller.APIError{Code: controller.CodeInternal, Message: "Kubernetes operation failed", Cause: err}
	}
	writeJSON(writer, versionDocument{GitVersion: result.GitVersion, GatewayVersion: handler.gatewayVersion})
	return nil
}

func (handler *Handler) namespaces(
	writer http.ResponseWriter,
	request *http.Request,
	client kubernetesclient.Interface,
	principal controller.Principal,
) *controller.APIError {
	options, apiError := listOptions(request)
	if apiError != nil {
		return apiError
	}
	result, err := client.CoreV1().Namespaces().List(request.Context(), options)
	if err != nil {
		return mapKubernetesError(err)
	}
	items := make([]namespaceDocument, 0, len(result.Items))
	subject := authorization.Subject{ID: principal.Subject, Groups: append([]string(nil), principal.Groups...)}
	for _, item := range result.Items {
		decision := handler.authorizer.Authorize(request.Context(), subject, authorization.Request{
			Operation: "list", Namespace: item.Name, ResourceKind: "capabilities",
		})
		if !decision.Allowed {
			continue
		}
		items = append(items, namespaceDocument{Name: item.Name, Status: string(item.Status.Phase)})
	}
	writeJSON(writer, listDocument[namespaceDocument]{
		Items: items, Continue: result.Continue, ResourceVersion: result.ResourceVersion,
	})
	return nil
}

func (handler *Handler) namespace(writer http.ResponseWriter, request *http.Request, client kubernetesclient.Interface, name string) *controller.APIError {
	result, err := client.CoreV1().Namespaces().Get(request.Context(), name, metav1.GetOptions{})
	if err != nil {
		return mapKubernetesError(err)
	}
	writeJSON(writer, namespaceDocument{Name: result.Name, Status: string(result.Status.Phase)})
	return nil
}

func (handler *Handler) pods(writer http.ResponseWriter, request *http.Request, client kubernetesclient.Interface, namespace string) *controller.APIError {
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
	writeJSON(writer, listDocument[podDocument]{Items: items, Continue: result.Continue, ResourceVersion: result.ResourceVersion})
	return nil
}

func (handler *Handler) pod(writer http.ResponseWriter, request *http.Request, client kubernetesclient.Interface, namespace, name string) *controller.APIError {
	result, err := client.CoreV1().Pods(namespace).Get(request.Context(), name, metav1.GetOptions{})
	if err != nil {
		return mapKubernetesError(err)
	}
	writeJSON(writer, podFromKubernetes(result))
	return nil
}

func (handler *Handler) services(writer http.ResponseWriter, request *http.Request, client kubernetesclient.Interface, namespace string) *controller.APIError {
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
	writeJSON(writer, listDocument[serviceDocument]{Items: items, Continue: result.Continue, ResourceVersion: result.ResourceVersion})
	return nil
}

func (handler *Handler) service(writer http.ResponseWriter, request *http.Request, client kubernetesclient.Interface, namespace, name string) *controller.APIError {
	result, err := client.CoreV1().Services(namespace).Get(request.Context(), name, metav1.GetOptions{})
	if err != nil {
		return mapKubernetesError(err)
	}
	writeJSON(writer, serviceFromKubernetes(result))
	return nil
}

func listOptions(request *http.Request) (metav1.ListOptions, *controller.APIError) {
	for key, values := range request.URL.Query() {
		if key != "limit" && key != "continue" && key != "labelSelector" && key != "fieldSelector" {
			return metav1.ListOptions{}, invalidQuery(key)
		}
		if len(values) != 1 {
			return metav1.ListOptions{}, &controller.APIError{
				Code: controller.CodeInvalidArgument, Field: key, Message: "query parameter must be provided once",
			}
		}
	}
	limit := defaultListLimit
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 || parsed > maximumListLimit {
			return metav1.ListOptions{}, &controller.APIError{
				Code: controller.CodeInvalidArgument, Field: "limit",
				Message: fmt.Sprintf("limit must be between 1 and %d", maximumListLimit),
			}
		}
		limit = parsed
	}
	continueToken := request.URL.Query().Get("continue")
	if len(continueToken) > maximumContinue || containsControl(continueToken) {
		return metav1.ListOptions{}, &controller.APIError{
			Code: controller.CodeInvalidArgument, Field: "continue", Message: "continue token is invalid",
		}
	}
	labelSelector := request.URL.Query().Get("labelSelector")
	if len(labelSelector) > 1024 || containsControl(labelSelector) {
		return metav1.ListOptions{}, &controller.APIError{
			Code: controller.CodeInvalidArgument, Field: "labelSelector", Message: "label selector is invalid",
		}
	}
	if labelSelector != "" {
		if _, err := labels.Parse(labelSelector); err != nil {
			return metav1.ListOptions{}, &controller.APIError{
				Code: controller.CodeInvalidArgument, Field: "labelSelector", Message: "label selector is invalid",
			}
		}
	}
	fieldSelector := request.URL.Query().Get("fieldSelector")
	if len(fieldSelector) > 1024 || containsControl(fieldSelector) {
		return metav1.ListOptions{}, &controller.APIError{
			Code: controller.CodeInvalidArgument, Field: "fieldSelector", Message: "field selector is invalid",
		}
	}
	if fieldSelector != "" {
		if _, err := fields.ParseSelector(fieldSelector); err != nil {
			return metav1.ListOptions{}, &controller.APIError{
				Code: controller.CodeInvalidArgument, Field: "fieldSelector", Message: "field selector is invalid",
			}
		}
	}
	return metav1.ListOptions{
		Limit: limit, Continue: continueToken, LabelSelector: labelSelector, FieldSelector: fieldSelector,
	}, nil
}

func capabilityNamespace(request *http.Request) (string, *controller.APIError) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "namespace" {
			return "", invalidQuery(key)
		}
		if len(values) != 1 {
			return "", &controller.APIError{
				Code: controller.CodeInvalidArgument, Field: key, Message: "query parameter must be provided once",
			}
		}
	}
	namespace := query.Get("namespace")
	if namespace == "" {
		return "", &controller.APIError{
			Code: controller.CodeInvalidArgument, Field: "namespace", Message: "namespace is required",
		}
	}
	if apiError := validateName("namespace", namespace, true); apiError != nil {
		return "", apiError
	}
	return namespace, nil
}

func rejectQuery(request *http.Request) *controller.APIError {
	for key := range request.URL.Query() {
		return invalidQuery(key)
	}
	return nil
}

func invalidQuery(field string) *controller.APIError {
	return &controller.APIError{Code: controller.CodeInvalidArgument, Field: field, Message: "query parameter is not supported"}
}

func validateNames(namespace, name string) *controller.APIError {
	if apiError := validateName("namespace", namespace, true); apiError != nil {
		return apiError
	}
	return validateName("name", name, false)
}

func validateName(field, value string, namespace bool) *controller.APIError {
	var problems []string
	if namespace {
		problems = validation.IsDNS1123Label(value)
	} else {
		problems = validation.IsDNS1123Subdomain(value)
	}
	if len(problems) != 0 {
		return &controller.APIError{Code: controller.CodeInvalidArgument, Field: field, Message: field + " is invalid"}
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
	return podDocument{
		Name: pod.Name, Namespace: pod.Namespace, Phase: string(pod.Status.Phase),
		PodIP: pod.Status.PodIP, NodeName: pod.Spec.NodeName, Ready: ready, Containers: containers, Ports: ports,
	}
}

func serviceFromKubernetes(service *corev1.Service) serviceDocument {
	ports := make([]servicePortDocument, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		ports = append(ports, servicePortDocument{
			Name: port.Name, Port: port.Port, Protocol: string(port.Protocol), TargetPort: port.TargetPort.String(),
		})
	}
	return serviceDocument{
		Name: service.Name, Namespace: service.Namespace, Type: string(service.Spec.Type),
		ClusterIP: service.Spec.ClusterIP, ExternalName: service.Spec.ExternalName, Ports: ports,
	}
}

func mapKubernetesError(err error) *controller.APIError {
	switch {
	case apierrors.IsNotFound(err):
		return &controller.APIError{Code: controller.CodeNotFound, Message: "resource not found", Cause: err}
	case apierrors.IsForbidden(err):
		return &controller.APIError{Code: controller.CodeForbidden, Message: "Kubernetes operation is not permitted", Cause: err}
	case apierrors.IsTooManyRequests(err):
		return &controller.APIError{Code: controller.CodeRateLimited, Message: "Kubernetes API rate limit exceeded", Cause: err}
	case apierrors.IsUnauthorized(err) || apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsServiceUnavailable(err) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return &controller.APIError{Code: controller.CodeUnavailable, Message: "Kubernetes API is unavailable", Cause: err}
	default:
		return &controller.APIError{Code: controller.CodeInternal, Message: "Kubernetes operation failed", Cause: err}
	}
}

func writeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(value)
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func routeParts(path string) ([]string, bool) {
	suffix, ok := strings.CutPrefix(path, controller.APIPathPrefix)
	if !ok || len(suffix) < 2 || suffix[0] != '/' || strings.HasSuffix(suffix, "/") || strings.Contains(suffix, "//") {
		return nil, false
	}
	return strings.Split(suffix[1:], "/"), true
}
