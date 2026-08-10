package portforwardapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"
)

type ClientProvider interface {
	ClientFor(authorization.Subject) (kubernetesclient.Interface, error)
}

type KubernetesResolver struct {
	provider ClientProvider
}

func NewKubernetesResolver(provider ClientProvider) (*KubernetesResolver, error) {
	if provider == nil {
		return nil, errors.New("Kubernetes client Provider is required")
	}
	return &KubernetesResolver{provider: provider}, nil
}

func (resolver *KubernetesResolver) Resolve(
	ctx context.Context,
	principal controller.Principal,
	namespace string,
	spec Spec,
) (Target, error) {
	client, err := resolver.provider.ClientFor(authorization.Subject{
		ID: principal.Subject, Groups: append([]string(nil), principal.Groups...),
	})
	if err != nil {
		return Target{}, err
	}
	switch spec.Kind {
	case "pod":
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, spec.Name, metav1.GetOptions{})
		if err != nil {
			return Target{}, err
		}
		if pod.DeletionTimestamp != nil || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			return Target{}, fmt.Errorf("Pod %s/%s is not running", namespace, spec.Name)
		}
		if net.ParseIP(strings.TrimSpace(pod.Status.PodIP)) == nil {
			return Target{}, fmt.Errorf("Pod %s/%s has no routable IP", namespace, spec.Name)
		}
		return Target{Host: pod.Status.PodIP, Port: spec.RemotePort}, nil
	case "service":
		service, err := client.CoreV1().Services(namespace).Get(ctx, spec.Name, metav1.GetOptions{})
		if err != nil {
			return Target{}, err
		}
		if service.Spec.Type == corev1.ServiceTypeExternalName || service.Spec.ClusterIP == "" || service.Spec.ClusterIP == corev1.ClusterIPNone || net.ParseIP(service.Spec.ClusterIP) == nil {
			return Target{}, fmt.Errorf("Service %s/%s has no routable ClusterIP", namespace, spec.Name)
		}
		protocol := corev1.Protocol(strings.ToUpper(spec.Protocol))
		for _, servicePort := range service.Spec.Ports {
			if servicePort.Port == int32(spec.RemotePort) && servicePort.Protocol == protocol {
				return Target{Host: service.Spec.ClusterIP, Port: spec.RemotePort}, nil
			}
		}
		return Target{}, fmt.Errorf("Service %s/%s does not expose %s/%d", namespace, spec.Name, spec.Protocol, spec.RemotePort)
	default:
		return Target{}, fmt.Errorf("unsupported Port Forward target kind %q", spec.Kind)
	}
}

var _ Resolver = (*KubernetesResolver)(nil)
