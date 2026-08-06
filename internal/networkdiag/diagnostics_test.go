package networkdiag

import (
	"net/netip"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
)

func TestAnalyzeRouteConflictsIgnoresLessSpecificRoutes(t *testing.T) {
	targets := []netip.Prefix{netip.MustParsePrefix("10.96.0.0/12")}
	routes := []hostRoute{
		{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Interface: "Ethernet"},
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Interface: "VPN"},
	}
	if issues := analyzeRouteConflicts(targets, routes); len(issues) != 0 {
		t.Fatalf("less-specific routes must not conflict: %#v", issues)
	}
}

func TestAnalyzeRouteConflictsFindsEqualAndMoreSpecificRoutes(t *testing.T) {
	targets := []netip.Prefix{netip.MustParsePrefix("10.96.0.0/12")}
	routes := []hostRoute{
		{Prefix: netip.MustParsePrefix("10.96.0.0/12"), Interface: "Mihomo"},
		{Prefix: netip.MustParsePrefix("10.100.0.0/16"), Interface: "Corporate VPN"},
		{Prefix: netip.MustParsePrefix("10.100.0.0/16"), Interface: "Corporate VPN"},
	}
	issues := analyzeRouteConflicts(targets, routes)
	if len(issues) != 2 {
		t.Fatalf("issues = %#v, want two deduplicated conflicts", issues)
	}
	if issues[0].Code != "route_overlap" || issues[0].Target != "10.96.0.0/12" {
		t.Fatalf("unexpected first issue: %#v", issues[0])
	}
}

func TestDiscoveryRoutesUsesServiceIPsOnlyAsFallback(t *testing.T) {
	withCIDR := discoveryRoutes(cluster.Discovery{
		PodCIDRs:     []string{"10.244.0.0/16"},
		ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs:   []string{"10.96.0.10"},
	})
	if len(withCIDR) != 2 {
		t.Fatalf("routes with Service CIDR = %v, want two aggregate routes", withCIDR)
	}
	withoutCIDR := discoveryRoutes(cluster.Discovery{
		ServiceIPs: []string{"10.96.0.10", "10.96.0.10"},
	})
	if len(withoutCIDR) != 1 || withoutCIDR[0].String() != "10.96.0.10/32" {
		t.Fatalf("fallback routes = %v, want deduplicated Service /32", withoutCIDR)
	}
}
