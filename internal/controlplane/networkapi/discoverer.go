package networkapi

import (
	"context"
	"errors"
	"net/netip"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type Provider interface {
	ClientFor(authorization.Subject) (kubernetesclient.Interface, error)
	SystemClient() (kubernetesclient.Interface, error)
}

// Discoverer performs every Kubernetes read inside Control Plane. Desktop clients
// receive only the normalized NetworkSpec and never require kubeconfig access.
type Discoverer struct {
	provider Provider
}

func NewDiscoverer(provider Provider) (*Discoverer, error) {
	if provider == nil {
		return nil, errors.New("network spec Kubernetes Provider is required")
	}
	return &Discoverer{provider: provider}, nil
}

func (discoverer *Discoverer) Discover(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
) (networkspec.Spec, error) {
	identityClient, err := discoverer.provider.ClientFor(authorization.Subject{
		ID: identity.Subject, Groups: identity.Groups,
	})
	if err != nil {
		return networkspec.Spec{}, errors.New(
			"create Kubernetes client for NetworkSpec discovery",
		)
	}
	systemClient, err := discoverer.provider.SystemClient()
	if err != nil {
		return networkspec.Spec{}, errors.New(
			"create system Kubernetes client for NetworkSpec discovery",
		)
	}
	return discover(ctx, identityClient, systemClient, namespace)
}

func discover(
	ctx context.Context,
	identityClient, systemClient kubernetesclient.Interface,
	namespace string,
) (networkspec.Spec, error) {
	podCIDRs := make(map[string]struct{})
	if nodes, err := systemClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		for _, node := range nodes.Items {
			addPrefix(podCIDRs, node.Spec.PodCIDR)
			for _, raw := range node.Spec.PodCIDRs {
				addPrefix(podCIDRs, raw)
			}
		}
	}
	pods, podErr := identityClient.CoreV1().
		Pods(namespace).
		List(ctx, metav1.ListOptions{})
	services, serviceErr := identityClient.CoreV1().
		Services(namespace).
		List(ctx, metav1.ListOptions{})
	if podErr != nil && serviceErr != nil {
		return networkspec.Spec{}, errors.New(
			"read namespace network resources",
		)
	}
	if pods == nil {
		pods = &corev1.PodList{}
	}
	if services == nil {
		services = &corev1.ServiceList{}
	}
	podIPs := make(map[string]struct{})
	for _, pod := range pods.Items {
		// Host-network addresses are node-level targets and must never be granted
		// by a namespace-scoped Pod listing.
		if pod.Spec.HostNetwork {
			continue
		}
		addAddress(podIPs, pod.Status.PodIP)
		for _, item := range pod.Status.PodIPs {
			addAddress(podIPs, item.IP)
		}
	}
	if len(podCIDRs) == 0 {
		for _, pod := range pods.Items {
			if pod.Spec.HostNetwork {
				continue
			}
			addInferredPrefix(podCIDRs, pod.Status.PodIP)
			for _, item := range pod.Status.PodIPs {
				addInferredPrefix(podCIDRs, item.IP)
			}
		}
	}

	serviceCIDRs := make(map[string]struct{})
	if list, err := systemClient.NetworkingV1().ServiceCIDRs().List(ctx, metav1.ListOptions{}); err == nil {
		for _, item := range list.Items {
			for _, raw := range item.Spec.CIDRs {
				addPrefix(serviceCIDRs, raw)
			}
		}
	}
	serviceIPs := make(map[string]struct{})
	for _, service := range services.Items {
		if service.Namespace == "default" && service.Name == "kubernetes" {
			continue
		}
		addServiceAddresses(serviceIPs, service)
	}
	dnsServer := ""
	for _, name := range []string{kubeDNSServiceName, coreDNSServiceName} {
		service, err := systemClient.CoreV1().
			Services("kube-system").
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			continue
		}
		addServiceAddresses(serviceIPs, *service)
		if address, parseErr := netip.ParseAddr(service.Spec.ClusterIP); parseErr == nil {
			dnsServer = address.Unmap().String()
		}
		break
	}
	if len(serviceCIDRs) == 0 {
		for raw := range serviceIPs {
			addInferredPrefix(serviceCIDRs, raw)
		}
	}
	return networkspec.Normalize(networkspec.Spec{
		Version: networkspec.Version, PodCIDRs: sortedKeys(podCIDRs), PodIPs: sortedKeys(podIPs),
		ServiceCIDRs: sortedKeys(serviceCIDRs),
		ServiceIPs:   sortedKeys(serviceIPs), DNSServer: dnsServer,
		ClusterDomains: discoverClusterDomains(ctx, systemClient),
	})
}
