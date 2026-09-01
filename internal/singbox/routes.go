package singbox

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
)

func clusterRoutes(network NetworkSpec) ([]string, error) {
	routeSet := make(map[netip.Prefix]struct{})
	for _, raw := range network.PodCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid pod CIDR %q: %w", raw, err)
		}
		routeSet[prefix.Masked()] = struct{}{}
	}
	// Exact observed Pod IPs are authorization targets and also act as routing
	// fallbacks when the cluster advertises a stale or incomplete Pod CIDR.
	for _, raw := range network.PodIPs {
		ip, err := netip.ParseAddr(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid pod IP %q: %w", raw, err)
		}
		routeSet[netip.PrefixFrom(ip, ip.BitLen())] = struct{}{}
	}
	for _, raw := range network.ServiceCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid service CIDR %q: %w", raw, err)
		}
		routeSet[prefix.Masked()] = struct{}{}
	}
	// Fall back to per-Service /32s when the cluster Service CIDR is unknown.
	if len(network.ServiceCIDRs) == 0 {
		for _, raw := range network.ServiceIPs {
			ip, err := netip.ParseAddr(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid service IP %q: %w", raw, err)
			}
			routeSet[netip.PrefixFrom(ip, ip.BitLen())] = struct{}{}
		}
	}
	if len(routeSet) == 0 {
		return nil, errors.New("cluster discovery returned no routable addresses")
	}
	return compactRoutes(routeSet), nil
}

func compactRoutes(routeSet map[netip.Prefix]struct{}) []string {
	prefixes := slices.Collect(maps.Keys(routeSet))
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int {
		if order := cmp.Compare(a.Addr().BitLen(), b.Addr().BitLen()); order != 0 {
			return order
		}
		if order := cmp.Compare(a.Bits(), b.Bits()); order != 0 {
			return order
		}
		return a.Addr().Compare(b.Addr())
	})

	covering := make([]netip.Prefix, 0, len(prefixes))
	routes := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		isCovered := slices.ContainsFunc(covering, func(candidate netip.Prefix) bool {
			return candidate.Addr().BitLen() == prefix.Addr().BitLen() && candidate.Contains(prefix.Addr())
		})
		if isCovered {
			continue
		}
		routes = append(routes, prefix.String())
		if prefix.Bits() < prefix.Addr().BitLen() {
			covering = append(covering, prefix)
		}
	}
	slices.Sort(routes)
	return routes
}

func validatePort(port int, label string) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s port must be between 1 and 65535", label)
	}
	return nil
}
