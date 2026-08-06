package cluster

import (
	"context"
	"fmt"
	"slices"

	clusterdiscovery "github.com/fengqi-dev/kube-loop/internal/cluster/discovery"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Discovery struct {
	PodCIDRs       []string `json:"podCIDRs"`
	ServiceCIDRs   []string `json:"serviceCIDRs"`
	ServiceIPs     []string `json:"serviceIPs"`
	DNSServer      string   `json:"dnsServer"`
	ClusterDomains []string `json:"clusterDomains,omitempty"`
	Pods           int      `json:"pods"`
	Services       int      `json:"services"`
	Deployments    int      `json:"deployments"`
}

// ServicePortInfo describes one Service port for the intercept UI.
type ServicePortInfo struct {
	Name     string `json:"name"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

// ServiceInfo is a ClusterIP Service that can be intercepted.
type ServiceInfo struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	ClusterIP string            `json:"clusterIP"`
	Ports     []ServicePortInfo `json:"ports"`
}

func (p *Provider) RESTConfig(contextName string) (*rest.Config, error) {
	overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
	cfg := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(p.loadingRules(), overrides)
	restConfig, err := cfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load context %q: %w", contextName, err)
	}
	p.mu.RLock()
	userAgent := p.userAgent
	p.mu.RUnlock()
	if userAgent == "" {
		userAgent = "kube-loop/dev"
	}
	restConfig.UserAgent = userAgent
	return restConfig, nil
}

func (p *Provider) client(contextName string) (kubernetes.Interface, error) {
	cfg, err := p.RESTConfig(contextName)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}
	return client, nil
}

func (p *Provider) Namespaces(ctx context.Context, contextName string) ([]string, error) {
	client, err := p.client(contextName)
	if err != nil {
		return nil, err
	}
	list, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.Name)
	}
	slices.Sort(names)
	return names, nil
}

func (p *Provider) ListServices(
	ctx context.Context, contextName, namespace string,
) ([]ServiceInfo, error) {
	listNS := apiNamespace(namespace)
	client, err := p.client(contextName)
	if err != nil {
		return nil, err
	}
	list, err := client.CoreV1().Services(listNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	refs := make([]*corev1.Service, 0, len(list.Items))
	for i := range list.Items {
		refs = append(refs, &list.Items[i])
	}
	return serviceInfosFromList(refs), nil
}

// apiNamespace maps UI namespace selection to the Kubernetes API namespace.
// "*" means all namespaces; empty falls back to default.
func apiNamespace(namespace string) string {
	if namespace == "*" {
		return ""
	}
	if namespace == "" {
		return "default"
	}
	return namespace
}

// Discover collects routable CIDRs / ClusterIPs. namespaces empty = all namespaces.
// Node / deployment / kube-system reads are best-effort so ns-scoped users can connect.
func (p *Provider) Discover(ctx context.Context, contextName string, namespaces []string) (Discovery, error) {
	client, err := p.client(contextName)
	if err != nil {
		return Discovery{}, err
	}
	result, err := clusterdiscovery.Discover(ctx, client, namespaces)
	if err != nil {
		return Discovery{}, err
	}
	dnsServer, clusterDomains, _ := p.discoverGatewayDNS(ctx, contextName)
	if result.DNSServer == "" {
		result.DNSServer = dnsServer
	}
	result.ClusterDomains = clusterDomains
	return Discovery{
		PodCIDRs:       result.PodCIDRs,
		ServiceCIDRs:   result.ServiceCIDRs,
		ServiceIPs:     result.ServiceIPs,
		DNSServer:      result.DNSServer,
		ClusterDomains: result.ClusterDomains,
		Pods:           result.Pods,
		Services:       result.Services,
		Deployments:    result.Deployments,
	}, nil
}
