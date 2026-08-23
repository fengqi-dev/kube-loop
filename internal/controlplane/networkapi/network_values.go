package networkapi

import (
	"net/netip"
	"slices"

	corev1 "k8s.io/api/core/v1"
)

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
