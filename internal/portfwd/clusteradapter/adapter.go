package clusteradapter

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
)

// Provider is the Kubernetes-facing contract needed to adapt cluster
// inventory and API-server port-forwarding to the portfwd use case.
type Provider interface {
	ListPods(context.Context, string, string) ([]cluster.PodInfo, error)
	ListServices(context.Context, string, string) ([]cluster.ServiceInfo, error)
	ResolveServiceBackend(
		context.Context, string, string, string, int32,
	) (string, uint16, error)
	StartPodPortForward(
		context.Context, string, string, string, uint16, uint16,
	) (cluster.PortForward, error)
}

type Adapter struct {
	provider Provider
}

func New(provider Provider) *Adapter {
	return &Adapter{provider: provider}
}

func (a *Adapter) ResolveServiceBackend(
	ctx context.Context, contextName, namespace, service string, port int32,
) (string, uint16, error) {
	return a.provider.ResolveServiceBackend(ctx, contextName, namespace, service, port)
}

func (a *Adapter) StartPodPortForward(
	ctx context.Context,
	contextName, namespace, pod string,
	localPort, remotePort uint16,
) (portfwd.Forwarder, error) {
	return a.provider.StartPodPortForward(
		ctx, contextName, namespace, pod, localPort, remotePort,
	)
}

func (a *Adapter) ResolveRoutedTarget(
	ctx context.Context, request portfwd.Request,
) (string, error) {
	switch request.Kind {
	case portfwd.KindPod:
		pods, err := a.provider.ListPods(ctx, request.Context, request.Namespace)
		if err != nil {
			return "", err
		}
		for _, pod := range pods {
			if pod.Namespace == request.Namespace && pod.Name == request.Name {
				if pod.IP == "" {
					return "", fmt.Errorf("pod %s/%s has no IP", request.Namespace, request.Name)
				}
				return net.JoinHostPort(pod.IP, strconv.Itoa(int(request.RemotePort))), nil
			}
		}
		return "", fmt.Errorf("pod %s/%s not found", request.Namespace, request.Name)
	case portfwd.KindService:
		services, err := a.provider.ListServices(ctx, request.Context, request.Namespace)
		if err != nil {
			return "", err
		}
		for _, service := range services {
			if service.Namespace == request.Namespace && service.Name == request.Name {
				if service.ClusterIP == "" {
					return "", fmt.Errorf(
						"service %s/%s has no ClusterIP", request.Namespace, request.Name,
					)
				}
				return net.JoinHostPort(
					service.ClusterIP, strconv.Itoa(int(request.RemotePort)),
				), nil
			}
		}
		return "", fmt.Errorf("service %s/%s not found", request.Namespace, request.Name)
	default:
		return "", fmt.Errorf("unsupported kind %q", request.Kind)
	}
}

var (
	_ Provider           = (*cluster.Provider)(nil)
	_ portfwd.ClusterAPI = (*Adapter)(nil)
)
