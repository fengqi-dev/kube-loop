package cluster

import (
	"reflect"
	"testing"
)

func TestParseClusterDNSConfig(t *testing.T) {
	server, domains, err := parseClusterDNSConfig(
		"nameserver 10.96.0.10\n" +
			"search default.svc.corp.internal svc.corp.internal corp.internal\n" +
			"options ndots:5\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	if server != "10.96.0.10" {
		t.Fatalf("server = %q", server)
	}
	wantDomains := []string{"cluster.local", "corp.internal"}
	if !reflect.DeepEqual(domains, wantDomains) {
		t.Fatalf("domains = %v, want %v", domains, wantDomains)
	}
}

func TestParseClusterDNSConfigDefaultsDomainAndSkipsInvalidServer(t *testing.T) {
	server, domains, err := parseClusterDNSConfig(
		"nameserver not-an-ip\nsearch invalid_search_domain\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	if server != "" {
		t.Fatalf("server = %q, want empty", server)
	}
	if !reflect.DeepEqual(domains, []string{"cluster.local"}) {
		t.Fatalf("domains = %v", domains)
	}
}
