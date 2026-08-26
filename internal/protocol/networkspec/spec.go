package networkspec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/dns"
)

const (
	Version         = 2
	MaximumItems    = 4096
	MaximumJSONSize = 256 << 10
)

// Spec is the minimal, immutable cluster-network snapshot consumed by a
// desktop Session and enforced by RelayTicket/Data Plane target validation.
type Spec struct {
	Version        int      `json:"version"`
	PodCIDRs       []string `json:"podCIDRs"`
	PodIPs         []string `json:"podIPs"`
	ServiceCIDRs   []string `json:"serviceCIDRs"`
	ServiceIPs     []string `json:"serviceIPs"`
	DNSServer      string   `json:"dnsServer,omitempty"`
	ClusterDomains []string `json:"clusterDomains"`
}

// Normalize validates untrusted discovery output and produces deterministic
// ordering suitable for persistence and hashing.
func Normalize(input Spec) (Spec, error) {
	if input.Version == 0 {
		input.Version = Version
	}
	if input.Version != Version {
		return Spec{}, fmt.Errorf("unsupported NetworkSpec version %d", input.Version)
	}
	itemCount := len(input.PodCIDRs) + len(input.PodIPs) + len(input.ServiceCIDRs) +
		len(input.ServiceIPs) + len(input.ClusterDomains)
	if itemCount > MaximumItems {
		return Spec{}, errors.New("NetworkSpec contains too many items")
	}
	podCIDRs, err := normalizeCIDRs(input.PodCIDRs, "Pod")
	if err != nil {
		return Spec{}, err
	}
	podIPs, err := normalizeAddresses(input.PodIPs, "Pod")
	if err != nil {
		return Spec{}, err
	}
	serviceCIDRs, err := normalizeCIDRs(input.ServiceCIDRs, "Service")
	if err != nil {
		return Spec{}, err
	}
	serviceIPs, err := normalizeAddresses(input.ServiceIPs, "Service")
	if err != nil {
		return Spec{}, err
	}
	dnsServer := ""
	if strings.TrimSpace(input.DNSServer) != "" {
		address, parseErr := netip.ParseAddr(strings.TrimSpace(input.DNSServer))
		if parseErr != nil || !allowedAddress(address.Unmap()) {
			return Spec{}, errors.New("NetworkSpec DNS server is invalid")
		}
		dnsServer = address.Unmap().String()
		if !slices.Contains(serviceIPs, dnsServer) {
			serviceIPs = append(serviceIPs, dnsServer)
			slices.Sort(serviceIPs)
		}
	}
	domains, err := dns.NormalizeClusterDomains(input.ClusterDomains)
	if err != nil {
		return Spec{}, err
	}
	if len(podCIDRs)+len(podIPs)+len(serviceCIDRs)+len(serviceIPs) == 0 {
		return Spec{}, errors.New("NetworkSpec contains no routable cluster network")
	}
	for _, podRaw := range podCIDRs {
		pod := netip.MustParsePrefix(podRaw)
		for _, serviceRaw := range serviceCIDRs {
			service := netip.MustParsePrefix(serviceRaw)
			if prefixesOverlap(pod, service) {
				return Spec{}, errors.New("NetworkSpec Pod and Service CIDRs overlap")
			}
		}
	}
	result := Spec{
		Version: Version, PodCIDRs: podCIDRs, PodIPs: podIPs, ServiceCIDRs: serviceCIDRs,
		ServiceIPs: serviceIPs, DNSServer: dnsServer, ClusterDomains: domains,
	}
	contents, err := json.Marshal(result)
	if err != nil || len(contents) > MaximumJSONSize {
		return Spec{}, errors.New("NetworkSpec is too large")
	}
	return result, nil
}

func CanonicalJSON(input Spec) ([]byte, error) {
	normalized, err := Normalize(input)
	if err != nil {
		return nil, err
	}
	contents, err := json.Marshal(normalized)
	if err != nil {
		return nil, errors.New("encode NetworkSpec")
	}
	return contents, nil
}

func Hash(input Spec) (string, error) {
	contents, err := CanonicalJSON(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:]), nil
}

func Decode(contents []byte) (Spec, error) {
	if len(contents) == 0 || len(contents) > MaximumJSONSize {
		return Spec{}, errors.New("stored NetworkSpec is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	var spec Spec
	if err := decoder.Decode(&spec); err != nil {
		return Spec{}, errors.New("decode stored NetworkSpec")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return Spec{}, errors.New("stored NetworkSpec contains trailing JSON")
	}
	return Normalize(spec)
}

func normalizeCIDRs(values []string, kind string) ([]string, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	seen := make(map[netip.Prefix]struct{}, len(values))
	for _, raw := range values {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("NetworkSpec %s CIDR is invalid", kind)
		}
		prefix = prefix.Masked()
		minimumBits := 32
		if prefix.Addr().Is4() {
			minimumBits = 8
		}
		if prefix.Bits() < minimumBits || !allowedPrefix(prefix) {
			return nil, fmt.Errorf("NetworkSpec %s CIDR is unsafe", kind)
		}
		if _, exists := seen[prefix]; !exists {
			seen[prefix] = struct{}{}
			prefixes = append(prefixes, prefix)
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
			if prefixesOverlap(existing, candidate) && existing.Bits() <= candidate.Bits() {
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
	return result, nil
}

func normalizeAddresses(values []string, kind string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		address, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || !allowedAddress(address.Unmap()) {
			return nil, fmt.Errorf("NetworkSpec %s IP is invalid", kind)
		}
		value := address.Unmap().String()
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result, nil
}

func allowedPrefix(prefix netip.Prefix) bool {
	if !allowedAddress(prefix.Addr()) {
		return false
	}
	unsafePrefixes := []string{
		"0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "224.0.0.0/4",
		"::/128", "::1/128", "fe80::/10", "ff00::/8",
	}
	for _, raw := range unsafePrefixes {
		if prefixesOverlap(prefix, netip.MustParsePrefix(raw)) {
			return false
		}
	}
	return true
}

func allowedAddress(address netip.Addr) bool {
	if !address.IsValid() || address.IsUnspecified() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsMulticast() {
		return false
	}
	if address.IsPrivate() {
		return true
	}
	return address.Is4() && netip.MustParsePrefix("100.64.0.0/10").Contains(address)
}

func prefixesOverlap(left, right netip.Prefix) bool {
	return left.Addr().BitLen() == right.Addr().BitLen() &&
		(left.Contains(right.Addr()) || right.Contains(left.Addr()))
}
