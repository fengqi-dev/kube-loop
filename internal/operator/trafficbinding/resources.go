package trafficbinding

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"net"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/internal/operator/api/v1alpha1"
)

const (
	serviceNameLabel     = "kubernetes.io/service-name"
	maximumSliceCount    = 64
	maximumEndpointCount = 4096
)

func (r *TrafficBindingReconciler) validateTarget(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	target := binding.Spec.Target
	port := binding.Spec.Ports[0]
	switch target.Kind {
	case trafficv1alpha1.TargetKindPod:
		pod := &corev1.Pod{}
		return r.Get(ctx, types.NamespacedName{Namespace: binding.Namespace, Name: target.Name}, pod)
	case trafficv1alpha1.TargetKindService:
		service := &corev1.Service{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: binding.Namespace, Name: target.Name}, service); err != nil {
			return err
		}
		_, err := servicePort(service, port.TargetPort, normalizedProtocol(port.Protocol))
		return err
	default:
		return permanentf("target kind %q is unsupported", target.Kind)
	}
}

func (r *TrafficBindingReconciler) reconcilePreview(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (*corev1.Service, error) {
	key := types.NamespacedName{Namespace: binding.Namespace, Name: binding.Spec.Preview.ServiceName}
	service := &corev1.Service{}
	err := r.Get(ctx, key, service)
	if apierrors.IsNotFound(err) {
		service = desiredPreviewService(binding)
		if err := controllerutil.SetControllerReference(binding, service, r.Scheme); err != nil {
			return nil, err
		}
		if err := r.Create(ctx, service); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return nil, permanentf("Service %s/%s already exists", key.Namespace, key.Name)
			}
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		if !metav1.IsControlledBy(service, binding) {
			return nil, permanentf("Service %s/%s is not owned by this TrafficBinding", key.Namespace, key.Name)
		}
		before := service.DeepCopy()
		applyBindingMetadata(service, binding)
		service.Spec.Type = corev1.ServiceTypeClusterIP
		service.Spec.Selector = nil
		service.Spec.Ports = desiredPreviewService(binding).Spec.Ports
		if !reflect.DeepEqual(before.Labels, service.Labels) ||
			!reflect.DeepEqual(before.Annotations, service.Annotations) ||
			!reflect.DeepEqual(before.Spec.Ports, service.Spec.Ports) ||
			before.Spec.Type != service.Spec.Type || before.Spec.Selector != nil {
			if err := r.Patch(ctx, service, client.MergeFrom(before)); err != nil {
				return nil, err
			}
		}
	}
	ports, err := resolvedServicePorts(service, binding.Spec.Ports)
	if err != nil {
		return nil, err
	}
	if err := r.reconcileRelaySlice(ctx, binding, service.Name, ports); err != nil {
		return nil, err
	}
	return service, nil
}

func desiredPreviewService(binding *trafficv1alpha1.TrafficBinding) *corev1.Service {
	ports := make([]corev1.ServicePort, 0, len(binding.Spec.Ports))
	for _, mapping := range binding.Spec.Ports {
		protocol := coreProtocol(normalizedProtocol(mapping.Protocol))
		ports = append(ports, corev1.ServicePort{
			Name: servicePortName(mapping), Protocol: protocol,
			Port: mapping.TargetPort, TargetPort: intstr.FromInt32(*mapping.RelayPort),
		})
	}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: binding.Spec.Preview.ServiceName, Namespace: binding.Namespace},
		Spec: corev1.ServiceSpec{
			Type:  corev1.ServiceTypeClusterIP,
			Ports: ports,
		},
	}
	applyBindingMetadata(service, binding)
	return service
}

func (r *TrafficBindingReconciler) captureService(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (*trafficv1alpha1.ServiceSnapshot, error) {
	target := binding.Spec.Target
	service := &corev1.Service{}
	key := types.NamespacedName{Namespace: binding.Namespace, Name: target.Name}
	if err := r.Get(ctx, key, service); err != nil {
		return nil, err
	}
	if service.Spec.ClusterIP == corev1.ClusterIPNone || service.Spec.Type == corev1.ServiceTypeExternalName {
		return nil, permanentf("Service %s/%s cannot be intercepted", key.Namespace, key.Name)
	}
	if _, err := resolvedServicePorts(service, binding.Spec.Ports); err != nil {
		return nil, err
	}
	if owner := service.Annotations[bindingUIDAnnotation]; owner != "" && owner != string(binding.UID) {
		return nil, permanentf("Service %s/%s is already owned by another TrafficBinding", key.Namespace, key.Name)
	}

	slices := &discoveryv1.EndpointSliceList{}
	if err := r.List(ctx, slices, client.InNamespace(binding.Namespace), client.MatchingLabels{serviceNameLabel: target.Name}); err != nil {
		return nil, err
	}
	if len(slices.Items) > maximumSliceCount {
		return nil, permanentf("Service %s/%s has more than %d EndpointSlices", key.Namespace, key.Name, maximumSliceCount)
	}
	snapshot := &trafficv1alpha1.ServiceSnapshot{
		ServiceName: target.Name, ServiceUID: service.UID,
		Selector: maps.Clone(service.Spec.Selector), HadEndpointSlices: len(slices.Items) > 0,
	}
	endpointCount := 0
	for index := range slices.Items {
		item := &slices.Items[index]
		endpointCount += len(item.Endpoints)
		if endpointCount > maximumEndpointCount {
			return nil, permanentf("Service %s/%s has more than %d endpoints", key.Namespace, key.Name, maximumEndpointCount)
		}
		snapshot.EndpointSlices = append(snapshot.EndpointSlices, trafficv1alpha1.EndpointSliceSnapshot{
			Name: item.Name, AddressType: item.AddressType,
			Labels: maps.Clone(item.Labels), Annotations: maps.Clone(item.Annotations),
			OwnerReferences: append([]metav1.OwnerReference(nil), item.OwnerReferences...),
			Endpoints:       cloneDiscoveryEndpoints(item.Endpoints), Ports: cloneDiscoveryPorts(item.Ports),
		})
	}
	legacy := &corev1.Endpoints{}
	if err := r.Get(ctx, key, legacy); err == nil {
		snapshot.HadEndpoints = true
		snapshot.EndpointSubsets = cloneEndpointSubsets(legacy.Subsets)
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}
	return snapshot, nil
}

func (r *TrafficBindingReconciler) reconcileIntercept(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	snapshot := binding.Status.Snapshot
	service := &corev1.Service{}
	key := types.NamespacedName{Namespace: binding.Namespace, Name: snapshot.ServiceName}
	if err := r.Get(ctx, key, service); err != nil {
		return err
	}
	if service.UID != snapshot.ServiceUID {
		return permanentf("Service %s/%s was replaced after rollback capture", key.Namespace, key.Name)
	}
	ports, err := resolvedServicePorts(service, binding.Spec.Ports)
	if err != nil {
		return err
	}
	if owner := service.Annotations[bindingUIDAnnotation]; owner != "" && owner != string(binding.UID) {
		return permanentf("Service %s/%s is owned by another TrafficBinding", key.Namespace, key.Name)
	}
	before := service.DeepCopy()
	applyBindingAnnotations(service, binding)
	service.Spec.Selector = map[string]string{}
	if !reflect.DeepEqual(before.Spec.Selector, service.Spec.Selector) ||
		!reflect.DeepEqual(before.Annotations, service.Annotations) {
		if err := r.Patch(ctx, service, client.MergeFrom(before)); err != nil {
			return err
		}
	}

	slices := &discoveryv1.EndpointSliceList{}
	if err := r.List(ctx, slices, client.InNamespace(binding.Namespace), client.MatchingLabels{serviceNameLabel: service.Name}); err != nil {
		return err
	}
	managedName := managedEndpointSliceName(binding)
	for index := range slices.Items {
		item := &slices.Items[index]
		if item.Name == managedName && ownedByBinding(item, binding) {
			continue
		}
		if err := r.Delete(ctx, item); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	legacy := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: service.Name, Namespace: binding.Namespace}}
	if err := r.Delete(ctx, legacy); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return r.reconcileRelaySlice(ctx, binding, service.Name, ports)
}

func (r *TrafficBindingReconciler) reconcileRelaySlice(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	serviceName string,
	ports []trafficv1alpha1.TrafficPort,
) error {
	desired := desiredRelaySlice(binding, serviceName, ports)
	if err := controllerutil.SetControllerReference(binding, desired, r.Scheme); err != nil {
		return err
	}
	current := &discoveryv1.EndpointSlice{}
	key := types.NamespacedName{Namespace: binding.Namespace, Name: desired.Name}
	if err := r.Get(ctx, key, current); apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	if !ownedByBinding(current, binding) || !metav1.IsControlledBy(current, binding) {
		return permanentf("EndpointSlice %s/%s is not owned by this TrafficBinding", key.Namespace, key.Name)
	}
	before := current.DeepCopy()
	current.Labels = maps.Clone(desired.Labels)
	current.Annotations = maps.Clone(desired.Annotations)
	current.AddressType = desired.AddressType
	current.Endpoints = desired.Endpoints
	current.Ports = desired.Ports
	if reflect.DeepEqual(before.Labels, current.Labels) &&
		reflect.DeepEqual(before.Annotations, current.Annotations) &&
		reflect.DeepEqual(before.AddressType, current.AddressType) &&
		reflect.DeepEqual(before.Endpoints, current.Endpoints) &&
		reflect.DeepEqual(before.Ports, current.Ports) {
		return nil
	}
	return r.Patch(ctx, current, client.MergeFrom(before))
}

func desiredRelaySlice(
	binding *trafficv1alpha1.TrafficBinding,
	serviceName string,
	ports []trafficv1alpha1.TrafficPort,
) *discoveryv1.EndpointSlice {
	ready := true
	endpointPorts := make([]discoveryv1.EndpointPort, 0, len(ports))
	for _, mapping := range ports {
		protocol := coreProtocol(normalizedProtocol(mapping.Protocol))
		var name *string
		if mapping.Name != "" {
			portName := mapping.Name
			name = &portName
		}
		port := *mapping.RelayPort
		endpointPorts = append(endpointPorts, discoveryv1.EndpointPort{
			Name: name, Protocol: &protocol, Port: &port,
		})
	}
	addressType := discoveryv1.AddressTypeIPv4
	if parsed := net.ParseIP(binding.Spec.Relay.Address); parsed != nil && parsed.To4() == nil {
		addressType = discoveryv1.AddressTypeIPv6
	}
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: managedEndpointSliceName(binding), Namespace: binding.Namespace,
			Labels: map[string]string{serviceNameLabel: serviceName},
		},
		AddressType: addressType,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{binding.Spec.Relay.Address},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		}},
		Ports: endpointPorts,
	}
	applyBindingMetadata(slice, binding)
	return slice
}

func (r *TrafficBindingReconciler) deletePreview(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	var result error
	slice := &discoveryv1.EndpointSlice{}
	sliceKey := types.NamespacedName{Namespace: binding.Namespace, Name: managedEndpointSliceName(binding)}
	if err := r.Get(ctx, sliceKey, slice); err == nil {
		if ownedByBinding(slice, binding) && metav1.IsControlledBy(slice, binding) {
			if deleteErr := r.Delete(ctx, slice); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				result = errorsJoin(result, deleteErr)
			}
		}
	} else if !apierrors.IsNotFound(err) {
		result = errorsJoin(result, err)
	}
	service := &corev1.Service{}
	serviceKey := types.NamespacedName{Namespace: binding.Namespace, Name: binding.Spec.Preview.ServiceName}
	if err := r.Get(ctx, serviceKey, service); err == nil {
		if ownedByBinding(service, binding) && metav1.IsControlledBy(service, binding) {
			if deleteErr := r.Delete(ctx, service); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				result = errorsJoin(result, deleteErr)
			}
		}
	} else if !apierrors.IsNotFound(err) {
		result = errorsJoin(result, err)
	}
	return result
}

func (r *TrafficBindingReconciler) restoreService(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	snapshot := binding.Status.Snapshot
	service := &corev1.Service{}
	key := types.NamespacedName{Namespace: binding.Namespace, Name: snapshot.ServiceName}
	if err := r.Get(ctx, key, service); apierrors.IsNotFound(err) {
		return r.deleteManagedSlice(ctx, binding)
	} else if err != nil {
		return err
	}
	if service.UID != snapshot.ServiceUID {
		return permanentf("Service %s/%s was replaced; refusing rollback", key.Namespace, key.Name)
	}
	if owner := service.Annotations[bindingUIDAnnotation]; owner != "" && owner != string(binding.UID) {
		return permanentf("Service %s/%s is owned by another TrafficBinding", key.Namespace, key.Name)
	}
	before := service.DeepCopy()
	service.Spec.Selector = maps.Clone(snapshot.Selector)
	removeBindingMetadata(service)
	if err := r.Patch(ctx, service, client.MergeFrom(before)); err != nil {
		return err
	}
	if err := r.deleteManagedSlice(ctx, binding); err != nil {
		return err
	}
	if snapshot.HadEndpointSlices {
		for index := range snapshot.EndpointSlices {
			if err := r.restoreEndpointSlice(ctx, binding.Namespace, &snapshot.EndpointSlices[index]); err != nil {
				return err
			}
		}
	}
	if snapshot.HadEndpoints {
		if err := r.restoreEndpoints(ctx, binding, snapshot); err != nil {
			return err
		}
	}
	return nil
}

func (r *TrafficBindingReconciler) deleteManagedSlice(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	slice := &discoveryv1.EndpointSlice{}
	key := types.NamespacedName{Namespace: binding.Namespace, Name: managedEndpointSliceName(binding)}
	if err := r.Get(ctx, key, slice); apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	if !ownedByBinding(slice, binding) {
		return permanentf("EndpointSlice %s/%s is no longer owned by this TrafficBinding", key.Namespace, key.Name)
	}
	return client.IgnoreNotFound(r.Delete(ctx, slice))
}

func (r *TrafficBindingReconciler) restoreEndpointSlice(
	ctx context.Context,
	namespace string,
	snapshot *trafficv1alpha1.EndpointSliceSnapshot,
) error {
	desired := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name: snapshot.Name, Namespace: namespace,
			Labels: maps.Clone(snapshot.Labels), Annotations: maps.Clone(snapshot.Annotations),
			OwnerReferences: append([]metav1.OwnerReference(nil), snapshot.OwnerReferences...),
		},
		AddressType: snapshot.AddressType,
		Endpoints:   cloneDiscoveryEndpoints(snapshot.Endpoints), Ports: cloneDiscoveryPorts(snapshot.Ports),
	}
	current := &discoveryv1.EndpointSlice{}
	key := types.NamespacedName{Namespace: namespace, Name: snapshot.Name}
	if err := r.Get(ctx, key, current); apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	current.Labels = desired.Labels
	current.Annotations = desired.Annotations
	current.OwnerReferences = desired.OwnerReferences
	current.AddressType = desired.AddressType
	current.Endpoints = desired.Endpoints
	current.Ports = desired.Ports
	return r.Update(ctx, current)
}

func (r *TrafficBindingReconciler) restoreEndpoints(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
	snapshot *trafficv1alpha1.ServiceSnapshot,
) error {
	desired := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: snapshot.ServiceName, Namespace: binding.Namespace},
		Subsets:    cloneEndpointSubsets(snapshot.EndpointSubsets),
	}
	current := &corev1.Endpoints{}
	key := types.NamespacedName{Namespace: binding.Namespace, Name: snapshot.ServiceName}
	if err := r.Get(ctx, key, current); apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}
	current.Subsets = desired.Subsets
	return r.Update(ctx, current)
}

func servicePort(
	service *corev1.Service,
	port int32,
	protocol trafficv1alpha1.TransportProtocol,
) (*corev1.ServicePort, error) {
	wanted := coreProtocol(protocol)
	for index := range service.Spec.Ports {
		candidate := &service.Spec.Ports[index]
		if candidate.Port == port && candidate.Protocol == wanted {
			return candidate, nil
		}
	}
	return nil, permanentf("Service %s/%s does not expose %s port %d", service.Namespace, service.Name, wanted, port)
}

func resolvedServicePorts(
	service *corev1.Service,
	mappings []trafficv1alpha1.TrafficPort,
) ([]trafficv1alpha1.TrafficPort, error) {
	result := make([]trafficv1alpha1.TrafficPort, 0, len(mappings))
	for _, mapping := range mappings {
		matched, err := servicePort(service, mapping.TargetPort, normalizedProtocol(mapping.Protocol))
		if err != nil {
			return nil, err
		}
		mapping.Name = matched.Name
		result = append(result, mapping)
	}
	return result, nil
}

func servicePortName(port trafficv1alpha1.TrafficPort) string {
	if port.Name != "" {
		return port.Name
	}
	return fmt.Sprintf("%s-%d", strings.ToLower(string(normalizedProtocol(port.Protocol))), port.TargetPort)
}

func coreProtocol(protocol trafficv1alpha1.TransportProtocol) corev1.Protocol {
	if protocol == trafficv1alpha1.TransportProtocolUDP {
		return corev1.ProtocolUDP
	}
	return corev1.ProtocolTCP
}

func applyBindingMetadata(object metav1.Object, binding *trafficv1alpha1.TrafficBinding) {
	labels := maps.Clone(object.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}
	labels[managedByLabel] = managedByValue
	labels[bindingNameLabel] = bindingLabelValue(binding.UID)
	object.SetLabels(labels)
	applyBindingAnnotations(object, binding)
}

func applyBindingAnnotations(object metav1.Object, binding *trafficv1alpha1.TrafficBinding) {
	annotations := maps.Clone(object.GetAnnotations())
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[bindingNameAnnotation] = binding.Name
	annotations[bindingUIDAnnotation] = string(binding.UID)
	annotations[bindingModeAnnotation] = string(binding.Spec.Mode)
	object.SetAnnotations(annotations)
}

func removeBindingMetadata(object metav1.Object) {
	labels := maps.Clone(object.GetLabels())
	delete(labels, bindingNameLabel)
	object.SetLabels(labels)
	annotations := maps.Clone(object.GetAnnotations())
	delete(annotations, bindingNameAnnotation)
	delete(annotations, bindingUIDAnnotation)
	delete(annotations, bindingModeAnnotation)
	object.SetAnnotations(annotations)
}

func ownedByBinding(object metav1.Object, binding *trafficv1alpha1.TrafficBinding) bool {
	return object.GetAnnotations()[bindingNameAnnotation] == binding.Name &&
		object.GetAnnotations()[bindingUIDAnnotation] == string(binding.UID)
}

func bindingLabelValue(uid types.UID) string {
	digest := sha256.Sum256([]byte(uid))
	return fmt.Sprintf("%x", digest[:16])
}

func managedEndpointSliceName(binding *trafficv1alpha1.TrafficBinding) string {
	return "kubeloop-" + bindingLabelValue(binding.UID)
}

func cloneDiscoveryEndpoints(source []discoveryv1.Endpoint) []discoveryv1.Endpoint {
	if len(source) == 0 {
		return nil
	}
	result := make([]discoveryv1.Endpoint, len(source))
	for index := range source {
		result[index] = *source[index].DeepCopy()
	}
	return result
}

func cloneDiscoveryPorts(source []discoveryv1.EndpointPort) []discoveryv1.EndpointPort {
	if len(source) == 0 {
		return nil
	}
	result := make([]discoveryv1.EndpointPort, len(source))
	for index := range source {
		result[index] = *source[index].DeepCopy()
	}
	return result
}

func cloneEndpointSubsets(source []corev1.EndpointSubset) []corev1.EndpointSubset {
	if len(source) == 0 {
		return nil
	}
	result := make([]corev1.EndpointSubset, len(source))
	for index := range source {
		result[index] = *source[index].DeepCopy()
	}
	return result
}

func errorsJoin(left, right error) error {
	if left == nil {
		return right
	}
	return fmt.Errorf("%v; %w", left, right)
}
