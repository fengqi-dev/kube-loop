package clusteradapter

import (
	"context"
	"maps"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/intercept"
)

// Provider is the Kubernetes-facing contract implemented by cluster.Provider.
// Keeping it here makes this package the only bridge between intercept use
// cases and Kubernetes resource types.
type Provider interface {
	GetService(context.Context, string, string, string) (*corev1.Service, error)
	ApplyServiceIntercept(
		context.Context, string, *cluster.ServiceInterceptSnapshot, string,
	) error
	RestoreServiceIntercept(
		context.Context, string, cluster.ServiceInterceptSnapshot,
	) error
	CreatePreviewService(
		context.Context, string, cluster.PreviewServiceSnapshot, string,
	) (*corev1.Service, error)
	DeletePreviewService(
		context.Context, string, cluster.PreviewServiceSnapshot,
	) error
}

type Adapter struct {
	provider Provider
}

func New(provider Provider) *Adapter {
	return &Adapter{provider: provider}
}

func (a *Adapter) GetService(
	ctx context.Context, contextName, namespace, name string,
) (*intercept.Service, error) {
	service, err := a.provider.GetService(ctx, contextName, namespace, name)
	if err != nil {
		return nil, err
	}
	return serviceModel(service), nil
}

func (a *Adapter) ApplyServiceIntercept(
	ctx context.Context,
	contextName string,
	request intercept.ServiceInterceptRequest,
) (intercept.Lease, []intercept.Backend, error) {
	snapshot := cluster.ServiceInterceptSnapshot{
		Namespace: request.Namespace,
		Service:   request.Service,
		Selector:  request.Selector,
		Ports:     clusterPorts(request.Ports),
		GatewayIP: request.GatewayIP,
	}
	if err := a.provider.ApplyServiceIntercept(ctx, contextName, &snapshot, request.ID); err != nil {
		return nil, nil, err
	}
	lease := &serviceLease{
		provider: a.provider, contextName: contextName, snapshot: snapshot,
	}
	return lease, snapshotBackends(snapshot), nil
}

func (a *Adapter) CreatePreviewService(
	ctx context.Context,
	contextName string,
	request intercept.PreviewServiceRequest,
) (*intercept.Service, intercept.Lease, error) {
	snapshot := cluster.PreviewServiceSnapshot{
		Namespace: request.Namespace,
		Service:   request.Service,
		Ports:     clusterPorts(request.Ports),
		GatewayIP: request.GatewayIP,
	}
	service, err := a.provider.CreatePreviewService(ctx, contextName, snapshot, request.ID)
	if err != nil {
		return nil, nil, err
	}
	lease := &previewLease{
		provider: a.provider, contextName: contextName, snapshot: snapshot,
	}
	return serviceModel(service), lease, nil
}

type serviceLease struct {
	provider    Provider
	contextName string
	snapshot    cluster.ServiceInterceptSnapshot
}

func (l *serviceLease) Release(ctx context.Context) error {
	return l.provider.RestoreServiceIntercept(ctx, l.contextName, l.snapshot)
}

type previewLease struct {
	provider    Provider
	contextName string
	snapshot    cluster.PreviewServiceSnapshot
}

func (l *previewLease) Release(ctx context.Context) error {
	return l.provider.DeletePreviewService(ctx, l.contextName, l.snapshot)
}

func serviceModel(service *corev1.Service) *intercept.Service {
	if service == nil {
		return nil
	}
	ports := make([]intercept.ServicePort, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		ports = append(ports, intercept.ServicePort{
			Name: port.Name, Protocol: protocolName(port.Protocol), Port: port.Port,
		})
	}
	selector := make(map[string]string, len(service.Spec.Selector))
	maps.Copy(selector, service.Spec.Selector)
	return &intercept.Service{
		Namespace: service.Namespace,
		Name:      service.Name,
		ClusterIP: service.Spec.ClusterIP,
		Selector:  selector,
		Ports:     ports,
	}
}

func clusterPorts(ports []intercept.InterceptPort) []cluster.InterceptPort {
	out := make([]cluster.InterceptPort, 0, len(ports))
	for _, port := range ports {
		out = append(out, cluster.InterceptPort{
			Name:        port.Name,
			Protocol:    corev1.Protocol(protocolName(corev1.Protocol(port.Protocol))),
			ServicePort: port.ServicePort,
			ListenPort:  port.ListenPort,
		})
	}
	return out
}

func snapshotBackends(snapshot cluster.ServiceInterceptSnapshot) []intercept.Backend {
	if snapshot.HasEndpointSlices {
		return sliceBackends(snapshot.EndpointSlices)
	}
	if snapshot.HasEndpoints {
		return endpointBackends(snapshot.EndpointsSubsets)
	}
	return nil
}

func sliceBackends(slices []discoveryv1.EndpointSlice) []intercept.Backend {
	var out []intercept.Backend
	for _, slice := range slices {
		ports := make([]intercept.BackendPort, 0, len(slice.Ports))
		for _, port := range slice.Ports {
			if port.Port == nil {
				continue
			}
			name := ""
			if port.Name != nil {
				name = *port.Name
			}
			protocol := corev1.ProtocolTCP
			if port.Protocol != nil {
				protocol = *port.Protocol
			}
			ports = append(ports, intercept.BackendPort{
				Name: name, Protocol: protocolName(protocol), Port: *port.Port,
			})
		}
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				continue
			}
			for _, address := range endpoint.Addresses {
				if address != "" {
					out = append(out, intercept.Backend{Address: address, Ports: ports})
				}
			}
		}
	}
	return out
}

func endpointBackends(subsets []corev1.EndpointSubset) []intercept.Backend {
	var out []intercept.Backend
	for _, subset := range subsets {
		ports := make([]intercept.BackendPort, 0, len(subset.Ports))
		for _, port := range subset.Ports {
			ports = append(ports, intercept.BackendPort{
				Name: port.Name, Protocol: protocolName(port.Protocol), Port: port.Port,
			})
		}
		for _, address := range subset.Addresses {
			if address.IP != "" {
				out = append(out, intercept.Backend{Address: address.IP, Ports: ports})
			}
		}
	}
	return out
}

func protocolName(protocol corev1.Protocol) string {
	if protocol == corev1.ProtocolUDP {
		return intercept.ProtocolUDP
	}
	return intercept.ProtocolTCP
}

var (
	_ Provider             = (*cluster.Provider)(nil)
	_ intercept.ClusterAPI = (*Adapter)(nil)
	_ intercept.Lease      = (*serviceLease)(nil)
	_ intercept.Lease      = (*previewLease)(nil)
)
