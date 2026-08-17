package gateway

import (
	"context"
	"net/netip"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type staticResolver map[string][]netip.Addr

func (resolver staticResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return resolver[host], nil
}

func authorizedTargetSpec(t *testing.T) networkspec.Spec {
	t.Helper()
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.1.20"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestResolveAuthorizedFiltersDNSAnswersAndServiceCIDR(t *testing.T) {
	server := NewServer(nil, 0)
	server.Resolver = staticResolver{
		"api.development.svc.cluster.local": {
			netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.96.1.20"),
		},
		"api.production.svc.cluster.local": {netip.MustParseAddr("10.96.2.30")},
	}
	spec := authorizedTargetSpec(t)
	address, err := server.resolveAuthorized(context.Background(), "api.development.svc.cluster.local", 443, spec)
	if err != nil || address != "10.96.1.20:443" {
		t.Fatalf("address = %q, %v", address, err)
	}
	for _, target := range []string{"10.96.0.1", "169.254.169.254", "8.8.8.8"} {
		if _, err := server.resolveAuthorized(context.Background(), target, 443, spec); err == nil {
			t.Fatalf("target %s allowed", target)
		}
	}
	if _, err := server.resolveAuthorized(
		context.Background(), "api.production.svc.cluster.local", 443, spec,
	); err == nil {
		t.Fatal("cross-namespace DNS target was allowed")
	}
}
