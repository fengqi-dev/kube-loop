package singbox

import (
	"cmp"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/dns"
	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

func ResolverDomains(
	namespace string,
	clusterDomains []string,
	hosts []sessionspec.HostAlias,
	extra ...string,
) []string {
	domains, err := dns.NormalizeClusterDomains(clusterDomains)
	if err != nil || len(domains) == 0 {
		domains = []string{dns.DefaultClusterDomain}
	}
	out := make([]string, 0, len(domains)*3+len(hosts)+len(extra)+1)
	seen := make(map[string]struct{}, len(domains)*3+len(hosts)+len(extra)+1)
	add := func(domain string) {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain == "" {
			return
		}
		if _, exists := seen[domain]; exists {
			return
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	for _, domain := range domains {
		add(domain)
		add("svc." + domain)
		if namespace != "" {
			add(namespace + ".svc." + domain)
		}
	}
	add("svc")
	for _, item := range hosts {
		add(item.Domain)
	}
	for _, domain := range extra {
		add(domain)
	}
	return out
}

// NormalizeHostAliases validates and canonicalizes host aliases.
// An empty input returns nil (clears config).
func NormalizeHostAliases(items []sessionspec.HostAlias) ([]sessionspec.HostAlias, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]sessionspec.HostAlias, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(item.Domain)), ".")
		if domain == "" {
			return nil, errors.New("host alias domain is required")
		}
		if strings.ContainsAny(domain, " \t/") {
			return nil, fmt.Errorf("invalid host alias domain %q", item.Domain)
		}
		if !dns.ValidClusterDomain(domain) {
			return nil, fmt.Errorf("invalid host alias domain %q", item.Domain)
		}
		ip, err := netip.ParseAddr(strings.TrimSpace(item.IP))
		if err != nil || !ip.Is4() {
			return nil, fmt.Errorf("invalid host alias IPv4 %q", item.IP)
		}
		if _, exists := seen[domain]; exists {
			return nil, fmt.Errorf("duplicate host alias domain %q", domain)
		}
		seen[domain] = struct{}{}
		out = append(out, sessionspec.HostAlias{Domain: domain, IP: ip.String()})
	}
	slices.SortFunc(out, func(left, right sessionspec.HostAlias) int {
		return cmp.Compare(left.Domain, right.Domain)
	})
	return out, nil
}

// SearchDomains returns Kubernetes-style DNS search suffixes for short names
// such as mysql, mysql.default, and mysql.default.svc.
func SearchDomains(namespace string, clusterDomains ...string) []string {
	if namespace == "" {
		namespace = defaultNamespace
	}
	return SearchDomainsForNamespaces([]string{namespace}, clusterDomains...)
}

// SearchDomainsForNamespaces returns Kubernetes-style DNS search suffixes for
// every visible namespace. Namespace order is preserved and duplicates are removed.
func SearchDomainsForNamespaces(namespaces []string, clusterDomains ...string) []string {
	if len(namespaces) == 0 {
		namespaces = []string{defaultNamespace}
	}
	domains, err := dns.NormalizeClusterDomains(clusterDomains)
	if err != nil || len(domains) == 0 {
		domains = []string{dns.DefaultClusterDomain}
	}
	out := make([]string, 0, len(domains)*(len(namespaces)+2))
	seen := make(map[string]struct{}, len(domains)*(len(namespaces)+2))
	add := func(domain string) {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if domain == "" {
			return
		}
		if _, ok := seen[domain]; ok {
			return
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	for _, domain := range domains {
		for _, namespace := range namespaces {
			namespace = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(namespace)), ".")
			if namespace != "" {
				add(namespace + ".svc." + domain)
			}
		}
		add("svc." + domain)
		add(domain)
	}
	return out
}
