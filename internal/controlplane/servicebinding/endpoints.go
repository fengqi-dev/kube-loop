// Package servicebinding contains the shared Kubernetes snapshot models and
// read-only capture helpers used by the control plane and operator.
package servicebinding

import (
	"context"
	"fmt"
	"maps"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	interceptManagedLabel     = "app.kubernetes.io/managed-by"
	interceptManagedValue     = "kubeloop"
	interceptServiceNameLabel = ServiceNameLabel
	annotationInterceptID     = "kubeloop.dev/intercept-id"
)

// ServiceNameLabel associates an EndpointSlice with its Service.
const ServiceNameLabel = "kubernetes.io/service-name"

// InterceptPort maps one Service port onto a Gateway listener.
type InterceptPort struct {
	Name        string          `json:"name"`
	Protocol    corev1.Protocol `json:"protocol"`
	ServicePort int32           `json:"servicePort"`
	ListenPort  int32           `json:"listenPort"`
}

// ServiceInterceptSnapshot stores the Service and endpoint state required by
// a TrafficBinding. The operator owns all mutations and restoration.
type ServiceInterceptSnapshot struct {
	Namespace         string                      `json:"namespace"`
	Service           string                      `json:"service"`
	Selector          map[string]string           `json:"selector,omitempty"`
	Ports             []InterceptPort             `json:"ports"`
	GatewayIP         string                      `json:"gatewayIP"`
	EndpointSlices    []discoveryv1.EndpointSlice `json:"endpointSlices,omitempty"`
	HasEndpointSlices bool                        `json:"hasEndpointSlices,omitempty"`
	EndpointsSubsets  []corev1.EndpointSubset     `json:"endpointsSubsets,omitempty"`
	HasEndpoints      bool                        `json:"hasEndpoints,omitempty"`
}

// CaptureServiceIntercept records the authoritative Service and endpoint state
// without mutating Kubernetes.
func CaptureServiceIntercept(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot *ServiceInterceptSnapshot,
) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is required")
	}
	if snapshot.Namespace == "" || snapshot.Service == "" {
		return fmt.Errorf("namespace and service are required")
	}
	if snapshot.GatewayIP == "" {
		return fmt.Errorf("gateway IP is required")
	}
	if len(snapshot.Ports) == 0 {
		return fmt.Errorf("at least one port mapping is required")
	}

	service, err := client.CoreV1().Services(snapshot.Namespace).Get(ctx, snapshot.Service, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get service: %w", err)
	}
	if service.Spec.ClusterIP == corev1.ClusterIPNone || service.Spec.Type == corev1.ServiceTypeExternalName {
		return fmt.Errorf("service %s/%s cannot be intercepted", snapshot.Namespace, snapshot.Service)
	}
	if service.Annotations[annotationInterceptID] != "" {
		return fmt.Errorf("service %s/%s is already intercepted", snapshot.Namespace, snapshot.Service)
	}
	snapshot.Selector = maps.Clone(service.Spec.Selector)
	return snapshotEndpoints(ctx, client, snapshot)
}

func snapshotEndpoints(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot *ServiceInterceptSnapshot,
) error {
	list, err := client.DiscoveryV1().EndpointSlices(snapshot.Namespace).List(
		ctx,
		metav1.ListOptions{LabelSelector: interceptServiceNameLabel + "=" + snapshot.Service},
	)
	if err != nil {
		return fmt.Errorf("list endpoint slices: %w", err)
	}
	snapshot.EndpointSlices = nil
	for _, item := range list.Items {
		if item.Labels[interceptManagedLabel] != interceptManagedValue {
			snapshot.EndpointSlices = append(snapshot.EndpointSlices, *item.DeepCopy())
		}
	}
	snapshot.HasEndpointSlices = len(snapshot.EndpointSlices) > 0
	snapshot.HasEndpoints = false
	snapshot.EndpointsSubsets = nil

	legacy, err := client.CoreV1().Endpoints(snapshot.Namespace).Get(ctx, snapshot.Service, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get endpoints: %w", err)
	}
	snapshot.HasEndpoints = true
	snapshot.EndpointsSubsets = cloneEndpointSubsets(legacy.Subsets)
	return nil
}

func cloneEndpointSubsets(subsets []corev1.EndpointSubset) []corev1.EndpointSubset {
	if len(subsets) == 0 {
		return nil
	}
	out := make([]corev1.EndpointSubset, len(subsets))
	for i, subset := range subsets {
		out[i] = *subset.DeepCopy()
	}
	return out
}
