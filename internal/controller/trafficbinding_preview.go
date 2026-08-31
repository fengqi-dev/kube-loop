package controller

import (
	"context"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

func (r *TrafficBindingReconciler) reconcilePreview(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) (*corev1.Service, error) {
	key := types.NamespacedName{Namespace: binding.Namespace, Name: binding.Spec.Preview.ServiceName}
	service := &corev1.Service{}
	err := r.Get(ctx, key, service)
	switch {
	case apierrors.IsNotFound(err):
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
	case err != nil:
		return nil, err
	default:
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
	if err := r.reconcileRelaySlice(ctx, binding, service, ports); err != nil {
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
		Name: binding.Spec.Preview.ServiceName, Namespace: binding.Namespace,
		Spec: corev1.ServiceSpec{
			Type:  corev1.ServiceTypeClusterIP,
			Ports: ports,
		},
	}
	applyBindingMetadata(service, binding)
	return service
}
func (r *TrafficBindingReconciler) deletePreview(
	ctx context.Context,
	binding *trafficv1alpha1.TrafficBinding,
) error {
	var result error
	slice := &discoveryv1.EndpointSlice{}
	sliceKey := types.NamespacedName{
		Namespace: binding.Namespace,
		Name:      managedEndpointSliceName(binding),
	}
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
