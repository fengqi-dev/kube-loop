package networkspec

import (
	"strings"
	"testing"
)

func TestNormalizeAndHashAreCanonical(t *testing.T) {
	left, err := Normalize(Spec{
		PodCIDRs:     []string{"10.2.1.0/24", "10.2.0.0/16"},
		PodIPs:       []string{"10.2.1.9", "10.2.1.9"},
		ServiceCIDRs: []string{"10.96.0.0/12"}, ServiceIPs: []string{"10.96.0.10", "10.96.0.10"},
		DNSServer: "10.96.0.11", ClusterDomains: []string{"Corp.Internal."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(left.PodCIDRs) != 1 || left.PodCIDRs[0] != "10.2.0.0/16" ||
		len(left.PodIPs) != 1 || left.PodIPs[0] != "10.2.1.9" || len(left.ServiceIPs) != 2 ||
		left.ClusterDomains[0] != "cluster.local" || left.ClusterDomains[1] != "corp.internal" {
		t.Fatalf("normalized = %#v", left)
	}
	right := Spec{
		Version: Version, PodCIDRs: []string{"10.2.0.0/16"}, PodIPs: []string{"10.2.1.9"},
		ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.11", "10.96.0.10"}, DNSServer: "10.96.0.11",
		ClusterDomains: []string{"cluster.local", "corp.internal"},
	}
	leftHash, _ := Hash(left)
	rightHash, _ := Hash(right)
	if leftHash != rightHash || len(leftHash) != 64 {
		t.Fatalf("hashes = %q %q", leftHash, rightHash)
	}
}

func TestNormalizeRejectsUnsafeOrAmbiguousNetworks(t *testing.T) {
	tests := []Spec{
		{},
		{PodCIDRs: []string{"0.0.0.0/0"}},
		{PodCIDRs: []string{"127.0.0.0/8"}},
		{PodCIDRs: []string{"10.0.0.0/8"}, ServiceCIDRs: []string{"10.96.0.0/12"}},
		{ServiceIPs: []string{"127.0.0.1"}},
		{PodCIDRs: []string{"8.8.8.0/24"}},
	}
	for _, test := range tests {
		if _, err := Normalize(test); err == nil {
			t.Fatalf("Normalize(%#v) succeeded", test)
		}
	}
}

func TestDecodeIsStrict(t *testing.T) {
	if _, err := Decode([]byte(`{"version":2,"podCIDRs":["10.2.0.0/16"],"podIPs":[],"serviceCIDRs":[],"serviceIPs":[],"clusterDomains":[],"unknown":true}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Decode([]byte(strings.Repeat("x", MaximumJSONSize+1))); err == nil {
		t.Fatal("oversized document accepted")
	}
}
