package gateway

import (
	"net/netip"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

func targetTestSpec(t *testing.T) networkspec.Spec {
	t.Helper()
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		PodIPs: []string{"10.2.4.5"}, ServiceIPs: []string{"10.96.1.20"}, DNSServer: "10.96.0.10",
		ClusterDomains: []string{"cluster.local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestAuthorizeAddressAllowsAnyIPWithinClusterCIDRs(t *testing.T) {
	spec := targetTestSpec(t)
	allowed := []struct {
		address string
		port    uint16
	}{
		{"10.2.4.5", 8080},  // exact PodIP
		{"10.2.99.1", 8080}, // cross-namespace PodIP in PodCIDR
		{"10.96.1.20", 443}, // exact ServiceIP
		{"10.96.0.1", 443},  // cross-namespace ClusterIP in ServiceCIDR
		{"10.96.0.10", 53},  // DNS server in ServiceCIDR
	}
	for _, item := range allowed {
		if err := AuthorizeAddress(spec, netip.MustParseAddr(item.address), item.port); err != nil {
			t.Fatalf("%s:%d denied: %v", item.address, item.port, err)
		}
	}
	denied := []struct {
		address string
		port    uint16
	}{
		{"10.96.0.10", 9153},    // DNS server on non-53 port
		{"169.254.169.254", 80}, // cloud metadata
		{"8.8.8.8", 53},         // public IP
		{"192.168.1.1", 8080},   // outside cluster CIDRs
	}
	for _, item := range denied {
		if err := AuthorizeAddress(spec, netip.MustParseAddr(item.address), item.port); err == nil {
			t.Fatalf("%s:%d allowed", item.address, item.port)
		}
	}
}

func TestAuthorizeAddressFallsBackToExactIPsWhenCIDRsEmpty(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodIPs:     []string{"10.2.4.5"},
		ServiceIPs: []string{"10.96.1.20"},
		DNSServer:  "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed := []struct {
		address string
		port    uint16
	}{
		{"10.2.4.5", 8080},
		{"10.96.1.20", 443},
		{"10.96.0.10", 53},
	}
	for _, item := range allowed {
		if err := AuthorizeAddress(spec, netip.MustParseAddr(item.address), item.port); err != nil {
			t.Fatalf("%s:%d denied: %v", item.address, item.port, err)
		}
	}
	denied := []struct {
		address string
		port    uint16
	}{
		{"10.2.4.6", 8080},
		{"10.96.0.1", 443},
	}
	for _, item := range denied {
		if err := AuthorizeAddress(spec, netip.MustParseAddr(item.address), item.port); err == nil {
			t.Fatalf("%s:%d allowed", item.address, item.port)
		}
	}
}

func TestAuthorizeDomainRequiresClusterSuffixAndBlocksAPIServer(t *testing.T) {
	spec := targetTestSpec(t)
	if host, err := AuthorizeDomain(
		spec,
		"api.development.svc.cluster.local.",
	); err != nil ||
		host != "api.development.svc.cluster.local" {
		t.Fatalf("cluster domain = %q, %v", host, err)
	}
	for _, host := range []string{"example.com", "cluster.local", "kubernetes.default.svc.cluster.local"} {
		if _, err := AuthorizeDomain(spec, host); err == nil {
			t.Fatalf("domain %q allowed", host)
		}
	}
}
