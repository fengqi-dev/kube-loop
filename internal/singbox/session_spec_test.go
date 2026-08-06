package singbox

import (
	"slices"
	"strings"
	"testing"
)

func validSessionSpec() SessionSpec {
	return SessionSpec{
		ID:               "session-abc123",
		PodCIDRs:         []string{"10.244.0.0/16"},
		ServiceCIDRs:     []string{"10.96.0.0/12"},
		ClusterDNSServer: "10.96.0.10",
		BridgeHost:       "127.0.0.1",
		BridgePort:       1080,
		ControllerPort:   9090,
		ControllerSecret: strings.Repeat("a", 64),
		DNSHost:          "127.0.0.1",
		DNSPort:          1053,
		PublicDNSPort:    53,
		TUNAddress:       "198.19.0.1/30",
		Namespace:        "default",
		Namespaces:       []string{"default", "payments"},
		Hosts:            []HostAlias{{Domain: "api.default.svc", IP: "10.96.0.1"}},
		TrafficPorts:     TrafficInboundPorts{Listen: 1081},
		TrafficPassword:  strings.Repeat("p", 64),
	}
}

func TestSessionSpecValidate(t *testing.T) {
	spec := validSessionSpec()
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	config, err := spec.GenerateConfig()
	if err != nil {
		t.Fatalf("GenerateConfig() error = %v", err)
	}
	if !strings.Contains(string(config), `"198.19.0.1/30"`) {
		t.Fatalf("config does not contain the validated TUN address")
	}
	dns, err := spec.DNS()
	if err != nil {
		t.Fatalf("DNS() error = %v", err)
	}
	if dns.Port != 53 || len(dns.Search) == 0 {
		t.Fatalf("unexpected DNS metadata: %#v", dns)
	}
	if !slices.Contains(dns.Search, "payments.svc.cluster.local") {
		t.Fatalf("DNS search domains do not include all namespaces: %#v", dns.Search)
	}
}

func TestSessionSpecRejectsPrivilegeBoundaryInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SessionSpec)
	}{
		{"path session ID", func(s *SessionSpec) { s.ID = "../../owned" }},
		{"remote bridge", func(s *SessionSpec) { s.BridgeHost = "10.0.0.1" }},
		{"remote DNS", func(s *SessionSpec) { s.DNSHost = "10.0.0.53" }},
		{"traffic port overlaps bridge", func(s *SessionSpec) {
			s.TrafficPorts.Listen = s.BridgePort
		}},
		{"arbitrary TUN range", func(s *SessionSpec) { s.TUNAddress = "10.0.0.1/24" }},
		{"resolver path", func(s *SessionSpec) {
			s.Hosts = []HostAlias{{Domain: "../resolver", IP: "10.96.0.1"}}
		}},
		{"namespace subdomain", func(s *SessionSpec) { s.Namespace = "team.default" }},
		{"long namespace", func(s *SessionSpec) { s.Namespace = strings.Repeat("a", 64) }},
		{"invalid namespace list", func(s *SessionSpec) { s.Namespaces = []string{"team.default"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := validSessionSpec()
			test.mutate(&spec)
			if err := spec.Validate(); err == nil {
				t.Fatal("Validate() unexpectedly succeeded")
			}
		})
	}
}
