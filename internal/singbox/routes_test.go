package singbox

import (
	"slices"
	"testing"
)

func TestClusterRoutesCompactsCoveredAddresses(t *testing.T) {
	t.Parallel()

	routes, err := clusterRoutes(NetworkSpec{
		PodCIDRs:     []string{"10.244.0.0/16", "fd00:10:244::/64"},
		PodIPs:       []string{"10.244.1.7", "10.245.1.7", "fd00:10:244::7", "fd00:10:245::7"},
		ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs:   []string{"10.96.0.10"},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"10.244.0.0/16",
		"10.245.1.7/32",
		"10.96.0.0/12",
		"fd00:10:244::/64",
		"fd00:10:245::7/128",
	}
	if !slices.Equal(routes, want) {
		t.Fatalf("clusterRoutes() = %v, want %v", routes, want)
	}
}
