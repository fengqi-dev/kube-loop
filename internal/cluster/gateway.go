package cluster

import (
	"context"
	"errors"

	clustergateway "github.com/fengqi-dev/kube-loop/internal/cluster/gatewayruntime"
	"github.com/fengqi-dev/kube-loop/internal/cluster/kubeportforward"
)

const (
	GatewayNamespace = clustergateway.Namespace
	GatewayName      = clustergateway.Name
	GatewayPort      = clustergateway.Port
	GatewayHTTPPort  = clustergateway.HTTPPort
)

// GatewayInfo identifies the running in-cluster Gateway Pod.
type GatewayInfo = clustergateway.Info

type PortForward interface {
	Address() string
	Close() error
}

func (p *Provider) EnsureGateway(ctx context.Context, contextName, image string) (GatewayInfo, error) {
	client, err := p.client(contextName)
	if err != nil {
		return GatewayInfo{}, err
	}
	return clustergateway.EnsureResource(
		ctx, client, image, p.GatewayNamespace(), p.GatewayName(),
	)
}

func (p *Provider) EnsureHTTPGateway(
	ctx context.Context, contextName, image, token, endpoint string,
) (GatewayInfo, error) {
	client, err := p.client(contextName)
	if err != nil {
		return GatewayInfo{}, err
	}
	return clustergateway.EnsureHTTPResource(
		ctx, client, image, p.GatewayNamespace(), p.GatewayName(), token, endpoint,
	)
}

// GetGateway finds an already-running Gateway Pod without installing resources.
func (p *Provider) GetGateway(ctx context.Context, contextName string) (GatewayInfo, error) {
	client, err := p.client(contextName)
	if err != nil {
		return GatewayInfo{}, err
	}
	return clustergateway.FindResource(ctx, client, p.GatewayNamespace(), p.GatewayName())
}

// GatewayInstallManifest returns a YAML snippet admins can apply when the user
// lacks install RBAC.
func GatewayInstallManifest(image string) string {
	return clustergateway.InstallManifest(image)
}

func GatewayInstallManifestNamed(image, name string) string {
	return clustergateway.InstallManifestNamed(image, name)
}

func GatewayInstallManifestResource(image, namespace, name string) string {
	return clustergateway.InstallManifestResource(image, namespace, name)
}

func GatewayHTTPInstallManifestResource(image, namespace, name, token, endpoint string) string {
	return clustergateway.HTTPIngressManifestResource(image, namespace, name, token, endpoint)
}

// StartPortForward opens an API Server port-forward to a Gateway Pod.
func (p *Provider) StartPortForward(
	ctx context.Context, contextName, podName string, remotePort uint16,
) (PortForward, error) {
	return p.StartPodPortForward(
		ctx, contextName, p.GatewayNamespace(), podName, 0, remotePort,
	)
}

// StartPodPortForward forwards 127.0.0.1:localPort to podName:remotePort.
// When localPort is 0, the OS allocates an ephemeral port.
func (p *Provider) StartPodPortForward(
	ctx context.Context,
	contextName, namespace, podName string,
	localPort, remotePort uint16,
) (PortForward, error) {
	if namespace == "" {
		return nil, errors.New("namespace is required")
	}
	if podName == "" {
		return nil, errors.New("pod name is required")
	}
	if remotePort == 0 {
		return nil, errors.New("remote port is required")
	}
	config, err := p.RESTConfig(contextName)
	if err != nil {
		return nil, err
	}
	return kubeportforward.Start(
		ctx, config, namespace, podName, localPort, remotePort,
	)
}
