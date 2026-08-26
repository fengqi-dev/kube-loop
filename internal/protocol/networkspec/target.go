package networkspec

import (
	"errors"
	"net/netip"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/dns"
)

// AuthorizeAddress applies the Data Plane target allowlist. Pod and Service
// CIDRs are routing metadata only: only Pod and Service IPs actually observed
// for the Session namespace are dialable. This prevents a namespace-scoped
// Session from using a broad cluster CIDR to reach another namespace.
func AuthorizeAddress(spec Spec, address netip.Addr, port uint16) error {
	address = address.Unmap()
	if port == 0 || !allowedAddress(address) {
		return errors.New("target address is not allowed by NetworkSpec")
	}
	if spec.DNSServer != "" {
		dnsServer, err := netip.ParseAddr(spec.DNSServer)
		if err == nil && dnsServer.Unmap() == address {
			if port != 53 {
				return errors.New("cluster DNS is only allowed on port 53")
			}
			return nil
		}
	}
	for _, raw := range spec.ServiceIPs {
		serviceIP, err := netip.ParseAddr(raw)
		if err == nil && serviceIP.Unmap() == address {
			return nil
		}
	}
	for _, raw := range spec.PodIPs {
		podIP, err := netip.ParseAddr(raw)
		if err == nil && podIP.Unmap() == address {
			return nil
		}
	}
	return errors.New("target address is not allowed by NetworkSpec")
}

// AuthorizeDomain limits DNS resolution to Kubernetes cluster domains. The
// resolved address must still pass AuthorizeAddress, which closes DNS-rebinding
// and cross-namespace Service access.
func AuthorizeDomain(spec Spec, host string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if !dns.ValidClusterDomain(host) || isKubernetesAPIName(host) {
		return "", errors.New("target domain is not allowed by NetworkSpec")
	}
	for _, domain := range spec.ClusterDomains {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if host != domain && strings.HasSuffix(host, "."+domain) {
			return host, nil
		}
	}
	return "", errors.New("target domain is not allowed by NetworkSpec")
}

func isKubernetesAPIName(host string) bool {
	return host == "kubernetes" || host == "kubernetes.default" || host == "kubernetes.default.svc" ||
		strings.HasPrefix(host, "kubernetes.default.svc.")
}
