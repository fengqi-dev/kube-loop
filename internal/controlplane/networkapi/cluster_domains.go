package networkapi

import (
	"context"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/dns"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"
)

const (
	maximumCorefileBytes     = 256 << 10
	maximumDiscoveredDomains = 16
)

// discoverClusterDomains reads only the fixed CoreDNS ConfigMap names through
// the Control Plane ServiceAccount. Corefile contents never leave the Control Plane;
// only bounded DNS-1123 domains are copied into NetworkSpec.
func discoverClusterDomains(
	ctx context.Context,
	client kubernetesclient.Interface,
) []string {
	domains := make([]string, 0, 2)
	seen := make(map[string]struct{})
	for _, name := range []string{coreDNSServiceName, kubeDNSServiceName} {
		configMap, err := client.CoreV1().
			ConfigMaps("kube-system").
			Get(ctx, name, metav1.GetOptions{})
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
			domain := strings.TrimSuffix(
				strings.ToLower(strings.TrimSpace(raw)),
				".",
			)
			if domain == "in-addr.arpa" || domain == "ip6.arpa" ||
				!dns.ValidClusterDomain(domain) {
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
