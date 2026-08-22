package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
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
		if err := r.Get(
			ctx,
			types.NamespacedName{Namespace: binding.Namespace, Name: target.Name},
			service,
		); err != nil {
			return err
		}
		_, err := servicePort(service, port.TargetPort, normalizedProtocol(port.Protocol))
		return err
	default:
		return permanentf("target kind %q is unsupported", target.Kind)
	}
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
