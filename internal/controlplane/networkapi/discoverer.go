package networkapi

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/dnsname"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"
)

type Provider interface {
	ClientFor(authorization.Subject) (kubernetesclient.Interface, error)
	SystemClient() (kubernetesclient.Interface, error)
}

const (
	maximumCorefileBytes     = 256 << 10
	maximumDiscoveredDomains = 16
)

// Discoverer performs every Kubernetes read inside Control Plane. Desktop clients
// receive only the normalized NetworkSpec and never require kubeconfig access.
type Discoverer struct {
	provider Provider
}

func NewDiscoverer(provider Provider) (*Discoverer, error) {
	if provider == nil {
		return nil, errors.New("NetworkSpec Kubernetes Provider is required")
	}
	return &Discoverer{provider: provider}, nil
}

func (discoverer *Discoverer) Discover(
	ctx context.Context,
	principal controlplaneapi.Principal,
	namespace string,
) (networkspec.Spec, error) {
	principalClient, err := discoverer.provider.ClientFor(authorization.Subject{
		ID: principal.Subject, Groups: principal.Groups,
	})
	if err != nil {
		return networkspec.Spec{}, errors.New("create Kubernetes client for NetworkSpec discovery")
	}
	systemClient, err := discoverer.provider.SystemClient()
	if err != nil {
		return networkspec.Spec{}, errors.New("create system Kubernetes client for NetworkSpec discovery")
	}
	return discover(ctx, principalClient, systemClient, namespace)
}

func discover(
	ctx context.Context,
	principalClient, systemClient kubernetesclient.Interface,
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
	pods, podErr := principalClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	services, serviceErr := principalClient.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if podErr != nil && serviceErr != nil {
		return networkspec.Spec{}, errors.New("read namespace network resources")
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
	for _, name := range []string{"kube-dns", "coredns"} {
		service, err := systemClient.CoreV1().Services("kube-system").Get(ctx, name, metav1.GetOptions{})
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

// discoverClusterDomains reads only the fixed CoreDNS ConfigMap names through
// the Control Plane ServiceAccount. Corefile contents never leave the Control Plane;
// only bounded DNS-1123 domains are copied into NetworkSpec.
func discoverClusterDomains(ctx context.Context, client kubernetesclient.Interface) []string {
	domains := make([]string, 0, 2)
	seen := make(map[string]struct{})
	for _, name := range []string{"coredns", "kube-dns"} {
		configMap, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			continue
		}
		for _, domain := range parseCoreDNSClusterDomains(configMap.Data["Corefile"]) {
			if _, exists := seen[domain]; exists {
				continue
			}
			seen[domain] = struct{}{}
			domains = append(domains, domain)
			if len(domains) == maximumDiscoveredDomains {
				return domains
			}
		}
	}
	return domains
}

func parseCoreDNSClusterDomains(corefile string) []string {
	if len(corefile) == 0 || len(corefile) > maximumCorefileBytes {
		return nil
	}
	domains := make([]string, 0, 2)
	seen := make(map[string]struct{})
	for line := range strings.SplitSeq(corefile, "\n") {
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = line[:comment]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "kubernetes" {
			continue
		}
		for _, raw := range fields[1:] {
			if raw == "{" {
				break
			}
			domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
			if domain == "in-addr.arpa" || domain == "ip6.arpa" || !dnsname.ValidClusterDomain(domain) {
				continue
			}
			if _, exists := seen[domain]; exists {
				continue
			}
			seen[domain] = struct{}{}
			domains = append(domains, domain)
			if len(domains) == maximumDiscoveredDomains {
				return domains
			}
		}
	}
	return domains
}

func addServiceAddresses(target map[string]struct{}, service corev1.Service) {
	addAddress(target, service.Spec.ClusterIP)
	for _, raw := range service.Spec.ClusterIPs {
		addAddress(target, raw)
	}
}

func addAddress(target map[string]struct{}, raw string) {
	if address, err := netip.ParseAddr(raw); err == nil {
		target[address.Unmap().String()] = struct{}{}
	}
}

func addPrefix(target map[string]struct{}, raw string) {
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		target[prefix.Masked().String()] = struct{}{}
	}
}

func addInferredPrefix(target map[string]struct{}, raw string) {
	address, err := netip.ParseAddr(raw)
	if err != nil {
		return
	}
	address = address.Unmap()
	bits := 64
	if address.Is4() {
		bits = 24
	}
	target[netip.PrefixFrom(address, bits).Masked().String()] = struct{}{}
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}
