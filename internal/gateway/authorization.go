package gateway

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/dns"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

// AuthorizeAddress applies the Data Plane target allowlist. Any address within
// the NetworkSpec PodCIDR or ServiceCIDR is allowed, enabling cross-namespace
// ClusterIP and PodIP access. Kubernetes API addresses and non-cluster private
// networks are still blocked.
func AuthorizeAddress(spec networkspec.Spec, address netip.Addr, port uint16) error {
	address = address.Unmap()
	if port == 0 || !networkspec.AllowedAddress(address) {
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
	for _, raw := range spec.ServiceCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err == nil && prefix.Contains(address) {
			return nil
		}
	}
	if len(spec.ServiceCIDRs) == 0 {
		for _, raw := range spec.ServiceIPs {
			serviceIP, err := netip.ParseAddr(raw)
			if err == nil && serviceIP.Unmap() == address {
				return nil
			}
		}
	}
	for _, raw := range spec.PodCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err == nil && prefix.Contains(address) {
			return nil
		}
	}
	if len(spec.PodCIDRs) == 0 {
		for _, raw := range spec.PodIPs {
			podIP, err := netip.ParseAddr(raw)
			if err == nil && podIP.Unmap() == address {
				return nil
			}
		}
	}
	return errors.New("target address is not allowed by NetworkSpec")
}

// AuthorizeDomain limits DNS resolution to Kubernetes cluster domains. The
// resolved address must still pass AuthorizeAddress, which closes DNS-rebinding
// and cross-namespace Service access.
func AuthorizeDomain(spec networkspec.Spec, host string) (string, error) {
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

type IPResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

func (s *Server) resolveAuthorized(
	ctx context.Context,
	host string,
	port uint16,
	spec networkspec.Spec,
) (string, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		if err := AuthorizeAddress(spec, address, port); err != nil {
			return "", err
		}
		return net.JoinHostPort(address.String(), strconv.Itoa(int(port))), nil
	}
	host, err := AuthorizeDomain(spec, host)
	if err != nil {
		return "", err
	}
	resolver := s.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return "", errors.New("resolve authorized cluster target")
	}
	allowed := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if AuthorizeAddress(spec, address, port) == nil {
			allowed = append(allowed, address)
		}
	}
	if len(allowed) == 0 {
		return "", errors.New("resolved target is not allowed by NetworkSpec")
	}
	slices.SortFunc(allowed, func(left, right netip.Addr) int { return left.Compare(right) })
	return net.JoinHostPort(allowed[0].String(), strconv.Itoa(int(port))), nil
}
