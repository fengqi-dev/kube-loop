package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
)

type ClientProvider interface {
	ClientFor(authorization.Subject) (clientset.Interface, error)
}

type ServiceResolver struct {
	provider ClientProvider
}

func NewServiceResolver(provider ClientProvider) (*ServiceResolver, error) {
	if provider == nil {
		return nil, errors.New("Kubernetes client Provider is required")
	}
	return &ServiceResolver{provider: provider}, nil
}

func (r *ServiceResolver) ResolveService(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	serviceName string,
	requested []trafficmodel.Port,
) (trafficmodel.ResolvedService, error) {
	client, err := r.provider.ClientFor(subjectFor(identity))
	if err != nil {
		return trafficmodel.ResolvedService{}, err
	}
	service, err := client.CoreV1().Services(namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return trafficmodel.ResolvedService{}, err
	}
	if service.Spec.Type == corev1.ServiceTypeExternalName ||
		service.Spec.ClusterIP == "" || service.Spec.ClusterIP == corev1.ClusterIPNone {
		return trafficmodel.ResolvedService{}, fmt.Errorf("Service %s/%s cannot be resolved", namespace, serviceName)
	}

	ports := make([]trafficmodel.Port, 0, len(requested))
	for _, requestedPort := range requested {
		matched := false
		for _, servicePort := range service.Spec.Ports {
			protocol := servicePort.Protocol
			if protocol == "" {
				protocol = corev1.ProtocolTCP
			}
			if servicePort.Port == requestedPort.ServicePort && strings.EqualFold(string(protocol), requestedPort.Protocol) {
				ports = append(ports, trafficmodel.Port{
					Name: servicePort.Name, ServicePort: servicePort.Port, Protocol: strings.ToLower(string(protocol)),
				})
				matched = true
				break
			}
		}
		if !matched {
			return trafficmodel.ResolvedService{}, errors.New("requested port does not match the authoritative Service")
		}
	}
	slices.SortFunc(ports, compareTrafficPorts)
	return trafficmodel.ResolvedService{
		Name: service.Name, ClusterIP: service.Spec.ClusterIP, Ports: ports,
	}, nil
}

func compareTrafficPorts(left, right trafficmodel.Port) int {
	if left.ServicePort != right.ServicePort {
		return int(left.ServicePort - right.ServicePort)
	}
	return strings.Compare(left.Protocol, right.Protocol)
}
