package controller

import (
	"context"
	"maps"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

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
	if err := r.List(
		ctx, slices, client.InNamespace(binding.Namespace),
		client.MatchingLabels{serviceNameLabel: target.Name},
	); err != nil {
		return nil, err
	}
	if len(slices.Items) > maximumSliceCount {
		return nil, permanentf(
			"Service %s/%s has more than %d EndpointSlices",
			key.Namespace,
			key.Name,
			maximumSliceCount,
		)
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
			return nil, permanentf(
				"Service %s/%s has more than %d endpoints",
				key.Namespace,
				key.Name,
				maximumEndpointCount,
			)
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
	if err := r.List(
		ctx, slices, client.InNamespace(binding.Namespace),
		client.MatchingLabels{serviceNameLabel: service.Name},
	); err != nil {
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
	legacy := &corev1.Endpoints{Name: service.Name, Namespace: binding.Namespace}
	if err := r.Delete(ctx, legacy); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return r.reconcileRelaySlice(ctx, binding, service, ports)
}
