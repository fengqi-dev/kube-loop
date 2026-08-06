package singbox

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/dnsname"
)

func ResolverDomains(namespace string, clusterDomains []string, hosts []HostAlias, extra ...string) []string {
	domains, err := dnsname.NormalizeClusterDomains(clusterDomains)
	if err != nil || len(domains) == 0 {
		domains = []string{dnsname.DefaultClusterDomain}
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
func NormalizeHostAliases(items []HostAlias) ([]HostAlias, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]HostAlias, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(item.Domain)), ".")
		if domain == "" {
			return nil, errors.New("host alias domain is required")
		}
		if strings.ContainsAny(domain, " \t/") {
			return nil, fmt.Errorf("invalid host alias domain %q", item.Domain)
		}
		if !dnsname.ValidClusterDomain(domain) {
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
		out = append(out, HostAlias{Domain: domain, IP: ip.String()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

// SearchDomains returns Kubernetes-style DNS search suffixes for short names
// such as mysql, mysql.default, and mysql.default.svc.
func SearchDomains(namespace string, clusterDomains ...string) []string {
	if namespace == "" {
		namespace = "default"
	}
	domains, err := dnsname.NormalizeClusterDomains(clusterDomains)
	if err != nil || len(domains) == 0 {
		domains = []string{dnsname.DefaultClusterDomain}
	}
	out := make([]string, 0, len(domains)*3)
	seen := make(map[string]struct{}, len(domains)*3)
	add := func(domain string) {
		if _, ok := seen[domain]; ok {
			return
		}
		seen[domain] = struct{}{}
		out = append(out, domain)
	}
	for _, domain := range domains {
		add(namespace + ".svc." + domain)
		add("svc." + domain)
		add(domain)
	}
	return out
}
