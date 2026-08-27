package relayregistry

import "testing"

func TestResolveAllowedHosts(t *testing.T) {
	hosts, err := ResolveAllowedHosts("relay-a.example, relay-b.example", "")
	if err != nil || len(hosts) != 2 {
		t.Fatalf("hosts=%v err=%v", hosts, err)
	}
	hosts, err = ResolveAllowedHosts("", "https://control.example.test/path")
	if err != nil || len(hosts) != 1 || hosts[0] != "control.example.test" {
		t.Fatalf("derived hosts=%v err=%v", hosts, err)
	}
}
