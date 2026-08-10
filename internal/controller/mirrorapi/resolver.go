package mirrorapi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type KubernetesProvider interface {
	ClientFor(authorization.Subject) (kubernetes.Interface, error)
}

type KubernetesServiceResolver struct{ provider KubernetesProvider }

func NewKubernetesServiceResolver(provider KubernetesProvider) (*KubernetesServiceResolver, error) {
	if provider == nil {
		return nil, errors.New("Kubernetes Provider is required")
	}
	return &KubernetesServiceResolver{provider: provider}, nil
}

func (resolver *KubernetesServiceResolver) ResolveService(
	ctx context.Context,
	principal controller.Principal,
	namespace, serviceName string,
	requested []Port,
) (Service, error) {
	client, err := resolver.provider.ClientFor(authorization.Subject{
		ID: principal.Subject, Groups: append([]string(nil), principal.Groups...),
	})
	if err != nil {
		return Service{}, err
	}
	service, err := client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return Service{}, err
	}
	if service.Spec.ClusterIP == "" || service.Spec.ClusterIP == corev1.ClusterIPNone || service.Spec.Type == corev1.ServiceTypeExternalName {
		return Service{}, fmt.Errorf("Service %s/%s cannot be mirrored", namespace, serviceName)
	}
	ports := make([]Port, 0, len(requested))
	for _, requestedPort := range requested {
		found := false
		for _, item := range service.Spec.Ports {
			protocol := item.Protocol
			if protocol == "" {
				protocol = corev1.ProtocolTCP
			}
			if item.Port == requestedPort.ServicePort && strings.EqualFold(string(protocol), requestedPort.Protocol) {
				ports = append(ports, Port{
					Name: item.Name, ServicePort: item.Port, Protocol: strings.ToLower(string(protocol)),
				})
				found = true
				break
			}
		}
		if !found {
			return Service{}, errors.New("Mirror port does not match the authoritative Service")
		}
	}
	slices.SortFunc(ports, comparePorts)
	return Service{Name: service.Name, ClusterIP: service.Spec.ClusterIP, Ports: ports}, nil
}

var _ ServiceResolver = (*KubernetesServiceResolver)(nil)
