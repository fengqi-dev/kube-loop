package singbox

import (
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
)

func clusterRoutes(network NetworkSpec) ([]string, error) {
	routeSet := make(map[string]struct{})
	for _, raw := range network.PodCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid pod CIDR %q: %w", raw, err)
		}
		routeSet[prefix.Masked().String()] = struct{}{}
	}
	for _, raw := range network.ServiceCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid service CIDR %q: %w", raw, err)
		}
		routeSet[prefix.Masked().String()] = struct{}{}
	}
	// Fall back to per-Service /32s when the cluster Service CIDR is unknown.
	if len(network.ServiceCIDRs) == 0 {
		for _, raw := range network.ServiceIPs {
			ip, err := netip.ParseAddr(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid service IP %q: %w", raw, err)
			}
			routeSet[netip.PrefixFrom(ip, ip.BitLen()).String()] = struct{}{}
		}
	}
	if len(routeSet) == 0 {
		return nil, errors.New("cluster discovery returned no routable addresses")
	}
	routes := slices.Sorted(maps.Keys(routeSet))
	return routes, nil
}

func validatePort(port int, label string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s port must be between 1 and 65535", label)
	}
	return nil
}
