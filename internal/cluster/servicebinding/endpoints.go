// Package servicebinding owns the Kubernetes resources and rollback snapshots
// used to bind Services to gateway listeners for intercepts and previews.
package servicebinding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

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
	annotationSelectorBackup  = "kubeloop.dev/selector-backup"
)

// ServiceNameLabel associates an EndpointSlice with its Service.
const ServiceNameLabel = "kubernetes.io/service-name"

// InterceptPort maps one Service port onto a unique Gateway listen port.
type InterceptPort struct {
	Name        string          `json:"name"`
	Protocol    corev1.Protocol `json:"protocol"`
	ServicePort int32           `json:"servicePort"`
	ListenPort  int32           `json:"listenPort"`
}

// ServiceInterceptSnapshot stores enough state to restore a Service after intercept.
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

// ApplyServiceIntercept replaces a Service's endpoints with a gateway endpoint.
func ApplyServiceIntercept(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot *ServiceInterceptSnapshot,
	interceptID string,
) error {
	return applyServiceIntercept(ctx, client, snapshot, interceptID)
}

// RestoreServiceIntercept restores the Service and endpoints captured by ApplyServiceIntercept.
func RestoreServiceIntercept(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot ServiceInterceptSnapshot,
) error {
	return restoreServiceIntercept(ctx, client, snapshot)
}

// GetService loads a Service from the selected Kubernetes client.
func GetService(
	ctx context.Context, client kubernetes.Interface, namespace, name string,
) (*corev1.Service, error) {
	service, err := client.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get service %s/%s: %w", namespace, name, err)
	}
	return service, nil
}

func applyServiceIntercept(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot *ServiceInterceptSnapshot,
	interceptID string,
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
	if service.Annotations[annotationInterceptID] != "" && service.Annotations[annotationInterceptID] != interceptID {
		return fmt.Errorf("service %s/%s is already intercepted", snapshot.Namespace, snapshot.Service)
	}
	endpointSliceNames, err := snapshotEndpointSlices(ctx, client, snapshot)
	if err != nil {
		return err
	}

	if service.Annotations == nil {
		service.Annotations = map[string]string{}
	}
	if service.Annotations[annotationSelectorBackup] == "" && len(service.Spec.Selector) > 0 {
		raw, err := json.Marshal(service.Spec.Selector)
		if err != nil {
			return fmt.Errorf("marshal selector: %w", err)
		}
		service.Annotations[annotationSelectorBackup] = string(raw)
	}
	service.Annotations[annotationInterceptID] = interceptID
	service.Spec.Selector = map[string]string{}

	if _, err := client.CoreV1().Services(snapshot.Namespace).Update(ctx, service, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("clear service selector: %w", err)
	}

	rollback := func(cause error) error {
		if restoreErr := restoreServiceIntercept(ctx, client, *snapshot); restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("rollback intercept: %w", restoreErr))
		}
		return cause
	}
	if err := deleteEndpointSlices(
		ctx, client, snapshot.Namespace, endpointSliceNames,
	); err != nil {
		return rollback(err)
	}

	slice := managedEndpointSlice(*snapshot, interceptID)
	_, err = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Create(ctx, slice, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_ = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Delete(
			ctx, slice.Name, metav1.DeleteOptions{},
		)
		_, err = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Create(ctx, slice, metav1.CreateOptions{})
	}
	if err != nil {
		return rollback(fmt.Errorf("create managed endpoint slice: %w", err))
	}
	return nil
}

func restoreServiceIntercept(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot ServiceInterceptSnapshot,
) error {
	service, err := client.CoreV1().Services(snapshot.Namespace).Get(ctx, snapshot.Service, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_ = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Delete(
				ctx, managedEndpointSliceName(snapshot.Service), metav1.DeleteOptions{},
			)
			return nil
		}
		return fmt.Errorf("get service for restore: %w", err)
	}

	selector := snapshot.Selector
	if len(selector) == 0 && service.Annotations[annotationSelectorBackup] != "" {
		if err := json.Unmarshal([]byte(service.Annotations[annotationSelectorBackup]), &selector); err != nil {
			return fmt.Errorf("decode selector backup: %w", err)
		}
	}
	service.Spec.Selector = selector
	if service.Annotations != nil {
		delete(service.Annotations, annotationInterceptID)
		delete(service.Annotations, annotationSelectorBackup)
	}
	if _, err := client.CoreV1().Services(snapshot.Namespace).Update(ctx, service, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("restore service selector: %w", err)
	}

	_ = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Delete(
		ctx, managedEndpointSliceName(snapshot.Service), metav1.DeleteOptions{},
	)
	if snapshot.HasEndpointSlices {
		if err := restoreEndpointSlices(ctx, client, snapshot); err != nil {
			return err
		}
	} else if err := restoreEndpoints(ctx, client, snapshot); err != nil {
		return err
	}
	return nil
}

func snapshotEndpointSlices(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot *ServiceInterceptSnapshot,
) ([]string, error) {
	list, err := client.DiscoveryV1().EndpointSlices(snapshot.Namespace).List(
		ctx,
		metav1.ListOptions{
			LabelSelector: interceptServiceNameLabel + "=" + snapshot.Service,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list endpoint slices: %w", err)
	}
	snapshot.EndpointSlices = nil
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Name)
		if item.Labels[interceptManagedLabel] != interceptManagedValue {
			snapshot.EndpointSlices = append(snapshot.EndpointSlices, *item.DeepCopy())
		}
	}
	snapshot.HasEndpointSlices = len(snapshot.EndpointSlices) > 0
	snapshot.HasEndpoints = false
	snapshot.EndpointsSubsets = nil
	return names, nil
}

func deleteEndpointSlices(
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	names []string,
) error {
	for _, name := range names {
		if err := client.DiscoveryV1().EndpointSlices(namespace).Delete(
			ctx, name, metav1.DeleteOptions{},
		); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete endpoint slice %s: %w", name, err)
		}
	}
	return nil
}

func restoreEndpointSlices(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot ServiceInterceptSnapshot,
) error {
	for _, saved := range snapshot.EndpointSlices {
		desired := endpointSliceForCreate(saved, snapshot.Namespace)
		_, err := client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Create(
			ctx, desired, metav1.CreateOptions{},
		)
		if apierrors.IsAlreadyExists(err) {
			current, getErr := client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Get(
				ctx, desired.Name, metav1.GetOptions{},
			)
			if getErr != nil {
				return fmt.Errorf("get endpoint slice %s for restore: %w", desired.Name, getErr)
			}
			current.Labels = desired.Labels
			current.Annotations = desired.Annotations
			current.OwnerReferences = desired.OwnerReferences
			current.AddressType = desired.AddressType
			current.Endpoints = desired.Endpoints
			current.Ports = desired.Ports
			_, err = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Update(
				ctx, current, metav1.UpdateOptions{},
			)
		}
		if err != nil {
			return fmt.Errorf("restore endpoint slice %s: %w", desired.Name, err)
		}
	}
	return nil
}

func endpointSliceForCreate(
	saved discoveryv1.EndpointSlice,
	namespace string,
) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:            saved.Name,
			Namespace:       namespace,
			Labels:          maps.Clone(saved.Labels),
			Annotations:     maps.Clone(saved.Annotations),
			OwnerReferences: append([]metav1.OwnerReference(nil), saved.OwnerReferences...),
			Finalizers:      append([]string(nil), saved.Finalizers...),
		},
		AddressType: saved.AddressType,
		Endpoints:   cloneDiscoveryEndpoints(saved.Endpoints),
		Ports:       cloneDiscoveryPorts(saved.Ports),
	}
}

func cloneDiscoveryEndpoints(
	endpoints []discoveryv1.Endpoint,
) []discoveryv1.Endpoint {
	if len(endpoints) == 0 {
		return nil
	}
	out := make([]discoveryv1.Endpoint, len(endpoints))
	for i := range endpoints {
		out[i] = *endpoints[i].DeepCopy()
	}
	return out
}

func cloneDiscoveryPorts(ports []discoveryv1.EndpointPort) []discoveryv1.EndpointPort {
	if len(ports) == 0 {
		return nil
	}
	out := make([]discoveryv1.EndpointPort, len(ports))
	for i := range ports {
		out[i] = *ports[i].DeepCopy()
	}
	return out
}

func restoreEndpoints(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot ServiceInterceptSnapshot,
) error {
	if !snapshot.HasEndpoints {
		return nil
	}
	desired := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshot.Service,
			Namespace: snapshot.Namespace,
		},
		Subsets: cloneEndpointSubsets(snapshot.EndpointsSubsets),
	}
	_, err := client.CoreV1().Endpoints(snapshot.Namespace).Create(ctx, desired, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		current, getErr := client.CoreV1().Endpoints(snapshot.Namespace).Get(
			ctx, snapshot.Service, metav1.GetOptions{},
		)
		if getErr != nil {
			return fmt.Errorf("get endpoints for restore: %w", getErr)
		}
		current.Subsets = cloneEndpointSubsets(snapshot.EndpointsSubsets)
		_, err = client.CoreV1().Endpoints(snapshot.Namespace).Update(ctx, current, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("restore endpoints: %w", err)
	}
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

func managedEndpointSliceName(serviceName string) string {
	name := serviceName + "-kubeloop"
	if len(name) > 63 {
		name = name[:63]
		name = strings.TrimRight(name, "-")
	}
	return name
}

func managedEndpointSlice(snapshot ServiceInterceptSnapshot, interceptID string) *discoveryv1.EndpointSlice {
	addressType := discoveryv1.AddressTypeIPv4
	ready := true
	ports := make([]discoveryv1.EndpointPort, 0, len(snapshot.Ports))
	for _, port := range snapshot.Ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		name := port.Name
		portNumber := port.ListenPort
		ports = append(ports, discoveryv1.EndpointPort{
			Name:     &name,
			Protocol: &protocol,
			Port:     &portNumber,
		})
	}
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      managedEndpointSliceName(snapshot.Service),
			Namespace: snapshot.Namespace,
			Labels: map[string]string{
				interceptServiceNameLabel: snapshot.Service,
				interceptManagedLabel:     interceptManagedValue,
				"app.kubernetes.io/name":  "kubeloop-intercept",
			},
			Annotations: map[string]string{
				annotationInterceptID: interceptID,
			},
		},
		AddressType: addressType,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{snapshot.GatewayIP},
			Conditions: discoveryv1.EndpointConditions{
				Ready:       &ready,
				Serving:     &ready,
				Terminating: new(false),
			},
		}},
		Ports: ports,
	}
}

// BuildInterceptPorts derives Service port mappings and allocates Gateway listen ports.
func BuildInterceptPorts(service *corev1.Service, allocate func(protocol corev1.Protocol) (int32, error)) ([]InterceptPort, error) {
	ports := make([]InterceptPort, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		listenPort, err := allocate(protocol)
		if err != nil {
			return nil, err
		}
		name := port.Name
		if name == "" {
			name = fmt.Sprintf("%s-%d", strings.ToLower(string(protocol)), port.Port)
		}
		ports = append(ports, InterceptPort{
			Name:        name,
			Protocol:    protocol,
			ServicePort: port.Port,
			ListenPort:  listenPort,
		})
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("service has no ports")
	}
	return ports, nil
}
