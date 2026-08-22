package controller

import (
	"context"
	"maps"
	"net"
	"reflect"

	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

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
		if createErr := r.Create(ctx, desired); createErr == nil {
			return nil
		} else if !apierrors.IsAlreadyExists(createErr) {
			return createErr
		}
		// The EndpointSlice controller can recreate a selector-backed slice
		// between our Get and Create after the Service selector is restored.
		// Reload and converge that object instead of failing finalizer cleanup.
		if getErr := r.Get(ctx, key, current); getErr != nil {
			return getErr
		}
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
		Name: managedEndpointSliceName(binding), Namespace: binding.Namespace,
		Labels:      map[string]string{serviceNameLabel: serviceName},
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
