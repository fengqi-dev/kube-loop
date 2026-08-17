package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	portforwardservice "github.com/fengqi-dev/kube-loop/internal/controlplane/portforwardapi/service"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PortForwardResolver struct {
	provider ClientProvider
}

func NewPortForwardResolver(provider ClientProvider) (*PortForwardResolver, error) {
	if provider == nil {
		return nil, errors.New("Kubernetes client Provider is required")
	}
	return &PortForwardResolver{provider: provider}, nil
}

func (r *PortForwardResolver) Resolve(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec portforwardservice.Spec,
) (portforwardservice.Target, error) {
	client, err := r.provider.ClientFor(subjectFor(identity))
	if err != nil {
		return portforwardservice.Target{}, err
	}
	switch spec.Kind {
	case "pod":
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, spec.Name, metav1.GetOptions{})
		if err != nil {
			return portforwardservice.Target{}, err
		}
		if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			return portforwardservice.Target{}, fmt.Errorf("Pod %s/%s is not running", namespace, spec.Name)
		}
		if net.ParseIP(strings.TrimSpace(pod.Status.PodIP)) == nil {
			return portforwardservice.Target{}, fmt.Errorf("Pod %s/%s has no routable IP", namespace, spec.Name)
		}
		return portforwardservice.Target{Host: pod.Status.PodIP, Port: spec.RemotePort}, nil
	case "service":
		service, err := client.CoreV1().Services(namespace).Get(ctx, spec.Name, metav1.GetOptions{})
		if err != nil {
			return portforwardservice.Target{}, err
		}
		if service.Spec.Type == corev1.ServiceTypeExternalName || service.Spec.ClusterIP == "" ||
			service.Spec.ClusterIP == corev1.ClusterIPNone || net.ParseIP(service.Spec.ClusterIP) == nil {
			return portforwardservice.Target{}, fmt.Errorf("Service %s/%s has no routable ClusterIP", namespace, spec.Name)
		}
		protocol := corev1.Protocol(strings.ToUpper(spec.Protocol))
		for _, servicePort := range service.Spec.Ports {
			if servicePort.Port == int32(spec.RemotePort) && servicePort.Protocol == protocol {
				return portforwardservice.Target{Host: service.Spec.ClusterIP, Port: spec.RemotePort}, nil
			}
		}
		return portforwardservice.Target{}, fmt.Errorf(
			"Service %s/%s does not expose %s/%d", namespace, spec.Name, spec.Protocol, spec.RemotePort,
		)
	default:
		return portforwardservice.Target{}, fmt.Errorf("unsupported Port Forward target kind %q", spec.Kind)
	}
}
