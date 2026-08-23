package runtime

import (
	"slices"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func TestDNSSearchCandidates(t *testing.T) {
	got := dnsSearchCandidates(
		"static-web.default.svc.",
		singbox.SearchDomains("default"),
	)
	want := []string{"static-web.default.svc.cluster.local."}
	if !slices.Equal(got, want) {
		t.Fatalf("partial Service candidates = %v, want %v", got, want)
	}
	fqdn := dnsSearchCandidates(
		"api.default.svc.cluster.local.",
		singbox.SearchDomains("default"),
	)
	if len(fqdn) != 1 || fqdn[0] != "api.default.svc.cluster.local." {
		t.Fatalf("FQDN should not expand: %v", fqdn)
	}
}
