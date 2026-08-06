package dnsname

import (
	"slices"
	"strings"
	"testing"
)

func TestValidClusterDomainUsesDNS1123Rules(t *testing.T) {
	tests := map[string]bool{
		"cluster.local":                 true,
		"dev.cluster.local":             true,
		"-cluster.local":                false,
		"cluster..local":                false,
		"cluster_local":                 false,
		strings.Repeat("a", 64) + ".io": false,
		strings.Repeat("a", 63) + "." +
			strings.Repeat("b", 63) + "." +
			strings.Repeat("c", 63) + "." +
			strings.Repeat("d", 61): true,
		strings.Repeat("a", 63) + "." +
			strings.Repeat("b", 63) + "." +
			strings.Repeat("c", 63) + "." +
			strings.Repeat("d", 62): false,
	}
	for value, want := range tests {
		if got := ValidClusterDomain(value); got != want {
			t.Errorf("ValidClusterDomain(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestNormalizeClusterDomainsCanonicalizesAndDeduplicates(t *testing.T) {
	got, err := NormalizeClusterDomains([]string{
		" DEV.Cluster.Local. ",
		"cluster.local",
		"",
		"dev.cluster.local",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"cluster.local", "dev.cluster.local"}
	if !slices.Equal(got, want) {
		t.Fatalf("NormalizeClusterDomains() = %v, want %v", got, want)
	}
}

func TestNormalizeClusterDomainsDefaultsAndRejectsInvalid(t *testing.T) {
	got, err := NormalizeClusterDomains(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{DefaultClusterDomain}) {
		t.Fatalf("default domains = %v", got)
	}

	got, err = NormalizeClusterDomains([]string{"valid.example", "bad_domain"})
	if err == nil {
		t.Fatal("expected invalid domain error")
	}
	if got != nil {
		t.Fatalf("partial result returned on error: %v", got)
	}
}
