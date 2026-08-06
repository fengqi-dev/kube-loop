package dnsname

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

const DefaultClusterDomain = "cluster.local"

// NormalizeClusterDomains validates and canonicalizes Kubernetes DNS domains.
// The default domain is always first, followed by custom domains in input order.
func NormalizeClusterDomains(domains []string) ([]string, error) {
	seen := make(map[string]struct{}, len(domains)+1)
	out := make([]string, 0, len(domains)+1)
	add := func(raw string) error {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if domain == "" {
			return nil
		}
		if !ValidClusterDomain(domain) {
			return fmt.Errorf("invalid cluster domain %q", raw)
		}
		if _, exists := seen[domain]; exists {
			return nil
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
		return nil
	}
	for _, domain := range domains {
		if err := add(domain); err != nil {
			return nil, err
		}
	}
	if err := add(DefaultClusterDomain); err != nil {
		return nil, err
	}
	if len(out) > 1 && out[0] != DefaultClusterDomain {
		rest := make([]string, 0, len(out)-1)
		for _, domain := range out {
			if domain != DefaultClusterDomain {
				rest = append(rest, domain)
			}
		}
		out = append([]string{DefaultClusterDomain}, rest...)
	}
	return out, nil
}

// ValidClusterDomain reports whether value is a normalized Kubernetes DNS domain.
func ValidClusterDomain(value string) bool {
	if len(validation.IsDNS1123Subdomain(value)) != 0 {
		return false
	}
	for label := range strings.SplitSeq(value, ".") {
		if len(validation.IsDNS1123Label(label)) != 0 {
			return false
		}
	}
	return true
}
