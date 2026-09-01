package controller

import (
	"context"
	"maps"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

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

func (r *TrafficBindingReconciler) cleanupInterceptWithoutSnapshot(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	if binding.Status.ServiceName == "" {
		return nil
	}
	service := &corev1.Service{}
	key := types.NamespacedName{Namespace: binding.Namespace, Name: binding.Status.ServiceName}
	if err := r.Get(ctx, key, service); apierrors.IsNotFound(err) {
		return r.deleteManagedSlice(ctx, binding)
	} else if err != nil {
		return err
	}
	annotations := service.GetAnnotations()
	if annotations[bindingNameAnnotation] == binding.Name ||
		annotations[bindingUIDAnnotation] == string(binding.UID) {
		return permanentf(
			"rollback snapshot is missing while Service %s/%s is still intercepted",
			key.Namespace,
			key.Name,
		)
	}
	return r.deleteManagedSlice(ctx, binding)
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
		Name: snapshot.Name, Namespace: namespace,
		Labels: maps.Clone(snapshot.Labels), Annotations: maps.Clone(snapshot.Annotations),
		OwnerReferences: append([]metav1.OwnerReference(nil), snapshot.OwnerReferences...),
		AddressType:     snapshot.AddressType,
		Endpoints:       cloneDiscoveryEndpoints(snapshot.Endpoints), Ports: cloneDiscoveryPorts(snapshot.Ports),
	}
	current := &discoveryv1.EndpointSlice{}
	key := types.NamespacedName{Namespace: namespace, Name: snapshot.Name}
	if err := r.Get(ctx, key, current); apierrors.IsNotFound(err) {
		if createErr := r.Create(ctx, desired); createErr == nil {
			return nil
		} else if !apierrors.IsAlreadyExists(createErr) {
			return createErr
		}
		// The EndpointSlice controller may recreate the selector-backed slice
		// between our read and create. Read that object and converge it below.
		if getErr := r.Get(ctx, key, current); getErr != nil {
			return getErr
		}
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
		Name: snapshot.ServiceName, Namespace: binding.Namespace,
		Subsets: cloneEndpointSubsets(snapshot.EndpointSubsets),
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
