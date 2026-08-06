package discovery

import (
	"net/netip"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	inferredIPv4Bits = 24
	inferredIPv6Bits = 64
	mergeFloorIPv4   = 12
	mergeFloorIPv6   = 48
)

func collectComponentCIDRs(pods []corev1.Pod) (map[string]struct{}, map[string]struct{}) {
	podCIDRs := make(map[string]struct{})
	serviceCIDRs := make(map[string]struct{})
	for _, pod := range pods {
		for _, container := range pod.Spec.Containers {
			args := append(slices.Clone(container.Command), container.Args...)
			for index := range args {
				name, value, found := strings.Cut(args[index], "=")
				if !found && index+1 < len(args) && strings.HasPrefix(args[index+1], "-") == false {
					value = args[index+1]
				}
				switch name {
				case "--cluster-cidr":
					addCIDRs(podCIDRs, value)
				case "--service-cluster-ip-range":
					addCIDRs(serviceCIDRs, value)
				}
			}
		}
	}
	return podCIDRs, serviceCIDRs
}

func inferPodCIDRs(pods []corev1.Pod) map[string]struct{} {
	var addresses []netip.Addr
	for _, pod := range pods {
		if pod.Spec.HostNetwork {
			continue
		}
		addAddress(&addresses, pod.Status.PodIP)
		for _, item := range pod.Status.PodIPs {
			addAddress(&addresses, item.IP)
		}
	}
	return inferCIDRs(addresses)
}

func inferServiceCIDRs(services []corev1.Service) map[string]struct{} {
	var addresses []netip.Addr
	for _, service := range services {
		addAddress(&addresses, service.Spec.ClusterIP)
		for _, raw := range service.Spec.ClusterIPs {
			addAddress(&addresses, raw)
		}
	}
	return inferCIDRs(addresses)
}

func addAddress(addresses *[]netip.Addr, raw string) {
	if address, err := netip.ParseAddr(strings.TrimSpace(raw)); err == nil {
		*addresses = append(*addresses, address.Unmap())
	}
}

// inferCIDRs follows kubevpn's bounded inference: observed IPv4/IPv6 addresses
// become /24 or /64 ranges and are merged to a common supernet only when that
// supernet remains no broader than /12 or /48.
func inferCIDRs(addresses []netip.Addr) map[string]struct{} {
	families := [2][]netip.Prefix{}
	for _, address := range addresses {
		bits := inferredIPv6Bits
		family := 1
		if address.Is4() {
			bits = inferredIPv4Bits
			family = 0
		}
		families[family] = append(families[family], netip.PrefixFrom(address, bits).Masked())
	}

	result := make(map[string]struct{})
	for family, prefixes := range families {
		if len(prefixes) == 0 {
			continue
		}
		first := prefixes[0].Addr()
		commonBits := prefixes[0].Bits()
		for _, prefix := range prefixes[1:] {
			commonBits = min(commonBits, commonPrefixBits(first, prefix.Addr()))
		}
		floor := mergeFloorIPv6
		if family == 0 {
			floor = mergeFloorIPv4
		}
		if commonBits >= floor {
			merged := netip.PrefixFrom(first, commonBits).Masked()
			result[merged.String()] = struct{}{}
			continue
		}
		for _, prefix := range prefixes {
			result[prefix.String()] = struct{}{}
		}
	}
	return result
}

func commonPrefixBits(left, right netip.Addr) int {
	leftBytes := left.As16()
	rightBytes := right.As16()
	offset := 0
	if left.Is4() && right.Is4() {
		offset = 12
	}
	bits := 0
	for index := offset; index < len(leftBytes); index++ {
		difference := leftBytes[index] ^ rightBytes[index]
		if difference == 0 {
			bits += 8
			continue
		}
		for mask := byte(0x80); difference&mask == 0; mask >>= 1 {
			bits++
		}
		break
	}
	return bits
}

func mergeCIDRs(target map[string]struct{}, sources ...map[string]struct{}) {
	for _, source := range sources {
		for cidr := range source {
			target[cidr] = struct{}{}
		}
	}
}

// compactCIDRs removes duplicates and ranges fully covered by a broader range.
func compactCIDRs(values map[string]struct{}) []string {
	prefixes := make([]netip.Prefix, 0, len(values))
	for raw := range values {
		if prefix, err := netip.ParsePrefix(raw); err == nil {
			prefixes = append(prefixes, prefix.Masked())
		}
	}
	slices.SortFunc(prefixes, func(left, right netip.Prefix) int {
		if left.Addr().BitLen() != right.Addr().BitLen() {
			return left.Addr().BitLen() - right.Addr().BitLen()
		}
		if left.Bits() != right.Bits() {
			return left.Bits() - right.Bits()
		}
		return left.Addr().Compare(right.Addr())
	})

	kept := make([]netip.Prefix, 0, len(prefixes))
	for _, candidate := range prefixes {
		covered := false
		for _, existing := range kept {
			if existing.Addr().BitLen() == candidate.Addr().BitLen() &&
				existing.Contains(candidate.Addr()) {
				covered = true
				break
			}
		}
		if !covered {
			kept = append(kept, candidate)
		}
	}
	result := make([]string, 0, len(kept))
	for _, prefix := range kept {
		result = append(result, prefix.String())
	}
	return result
}
