package singbox

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

func TestGenerateRoutesOnlyClusterTraffic(t *testing.T) {
	content, err := Generate(NetworkSpec{
		PodCIDRs:     []string{"10.244.0.0/16"},
		PodIPs:       []string{"10.244.1.7", "10.245.7.9"},
		ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs:   []string{"10.96.0.10", "10.96.0.1", "10.105.153.132"},
		DNSServer:    "10.96.0.10",
	}, Options{
		BridgePort: 17890, ControllerPort: 19090, ControllerSecret: "test-secret",
		DNSPort: 1053, Namespace: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	required := []string{
		`"type": "tun"`,
		`"auto_route": true`,
		`"10.244.0.0/16"`,
		`"10.245.7.9/32"`,
		`"10.96.0.0/12"`,
		`"cluster.local"`,
		`"prefer_ipv4"`,
		`"tag": "kubernetes"`,
		`"type": "socks"`,
		`"network": [`,
		`"udp"`,
		`"final": "direct"`,
		`"find_process": true`,
		`"external_controller"`,
	}
	for _, item := range []string{`"10.96.0.1/32"`, `"10.105.153.132/32"`} {
		if strings.Contains(text, item) {
			t.Errorf("per-IP route %s should be omitted when Service CIDR is present:\n%s", item, text)
		}
	}
	if strings.Contains(text, `"10.244.1.7/32"`) {
		t.Fatalf("Pod /32 covered by Pod CIDR should be omitted:\n%s", text)
	}
	for _, item := range required {
		if !strings.Contains(text, item) {
			t.Errorf("generated config does not contain %q:\n%s", item, text)
		}
	}
	forbidden := []string{
		`"0.0.0.0/1"`,
		`"128.0.0.0/1"`,
		`114.114.114.114`,
		`fake-ip`,
		`fake_ip`,
	}
	for _, item := range forbidden {
		if strings.Contains(text, item) {
			t.Errorf("generated config unexpectedly contains %q:\n%s", item, text)
		}
	}
	if runtime.GOOS == "linux" {
		if !strings.Contains(text, `"auto_redirect": true`) {
			t.Fatalf("linux tun config must enable auto_redirect:\n%s", text)
		}
	} else if !strings.Contains(text, `"auto_redirect": false`) {
		t.Fatalf("non-linux tun config must disable auto_redirect:\n%s", text)
	}

	var parsed map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatal(err)
	}
	inbounds, _ := parsed["inbounds"].([]any)
	var routeAddress []any
	for _, inbound := range inbounds {
		item, _ := inbound.(map[string]any)
		if item["type"] == "tun" {
			routeAddress, _ = item["route_address"].([]any)
			wantStrictRoute := runtime.GOOS != "windows"
			if got, _ := item["strict_route"].(bool); got != wantStrictRoute {
				t.Fatalf("strict_route = %v, want %v on %s", got, wantStrictRoute, runtime.GOOS)
			}
		}
	}
	if len(routeAddress) == 0 {
		t.Fatal("tun route_address missing")
	}
	for _, route := range routeAddress {
		value, _ := route.(string)
		if value == "0.0.0.0/1" || value == "128.0.0.0/1" {
			t.Fatalf("global route leaked into tun route_address: %v", routeAddress)
		}
	}
	route, _ := parsed["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	udpRuleFound := false
	kubernetesLogicalFound := false
	for _, rawRule := range rules {
		rule, _ := rawRule.(map[string]any)
		if rule[configTypeKey] == "logical" {
			if rule[configOutboundKey] != KubernetesOutbound {
				continue
			}
			kubernetesLogicalFound = true
			subRules, _ := rule["rules"].([]any)
			for _, rawSub := range subRules {
				sub, _ := rawSub.(map[string]any)
				networks, _ := sub["network"].([]any)
				for _, network := range networks {
					if network == "udp" {
						udpRuleFound = true
					}
				}
			}
		}
	}
	if !kubernetesLogicalFound {
		t.Fatal("cluster routes must be grouped in a Kubernetes logical rule")
	}
	if !udpRuleFound {
		t.Fatal("UDP route must use the Kubernetes SOCKS outbound")
	}
}

func TestGenerateRejectsInvalidDiscovery(t *testing.T) {
	_, err := Generate(NetworkSpec{PodCIDRs: []string{"not-a-cidr"}}, Options{
		BridgePort: 17890, ControllerPort: 19090, ControllerSecret: "secret",
	})
	if err == nil {
		t.Fatal("expected invalid CIDR error")
	}
}

func TestGenerateFixedTrafficInbounds(t *testing.T) {
	content, err := Generate(NetworkSpec{
		PodCIDRs: []string{"10.244.0.0/16"},
	}, Options{
		BridgePort: 17890, ControllerPort: 19090, ControllerSecret: "test-secret",
		DNSPort:         1053,
		TrafficPorts:    sessionspec.TrafficInboundPorts{Listen: 18081},
		TrafficPassword: "traffic-password-1234567890123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Count(text, `"type": "socks"`) != 2 {
		// traffic-in + kubernetes outbound socks
		t.Fatalf("expected one traffic socks inbound (+ kubernetes outbound):\n%s", text)
	}
	if strings.Contains(text, `"tag": "portfwd-in"`) ||
		strings.Contains(text, `"tag": "exchange-in"`) ||
		strings.Contains(text, `"tag": "preview-in"`) {
		t.Fatalf("legacy per-feature inbounds must be merged into traffic-in:\n%s", text)
	}
	for _, item := range []string{
		`"tag": "traffic-in"`,
		`"listen_port": 18081`,
		`"username": "kube-loop"`,
		`"auth_user"`,
		`"tag": "local"`,
	} {
		if !strings.Contains(text, item) {
			t.Fatalf("generated config missing %q:\n%s", item, text)
		}
	}
	for _, item := range []string{
		`"username": "exchange"`,
		`"username": "preview"`,
		`"username": "mirror-shadow"`,
	} {
		if strings.Contains(text, item) {
			t.Fatalf("dyed traffic users must be merged into the single local user, found %q:\n%s", item, text)
		}
	}
	if strings.Contains(text, `"username": "port-forward"`) {
		t.Fatalf("Port Forward must use the Data Plane SOCKS bridge, not traffic-in:\n%s", text)
	}
	if strings.Contains(text, `"username": "mirror-primary"`) {
		t.Fatalf("mirror-primary must use Gateway dial, not traffic-in:\n%s", text)
	}
}

func TestResolverDomains(t *testing.T) {
	got := ResolverDomains("demo", nil, nil)
	want := []string{"cluster.local", "svc.cluster.local", "demo.svc.cluster.local", "svc"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ResolverDomains = %v, want %v", got, want)
	}
	withHosts := ResolverDomains(
		"demo",
		[]string{"cluster.local"},
		[]sessionspec.HostAlias{{Domain: "app.dev", IP: "10.96.0.50"}},
	)
	if !strings.Contains(strings.Join(withHosts, ","), "app.dev") {
		t.Fatalf("ResolverDomains missing host alias: %v", withHosts)
	}
	custom := ResolverDomains("demo", []string{"corp.local"}, nil)
	if !strings.Contains(strings.Join(custom, ","), "corp.local") ||
		!strings.Contains(strings.Join(custom, ","), "cluster.local") {
		t.Fatalf("ResolverDomains custom domains: %v", custom)
	}
}

func TestGenerateHostAliases(t *testing.T) {
	content, err := Generate(NetworkSpec{
		PodCIDRs:     []string{"10.244.0.0/16"},
		ServiceCIDRs: []string{"10.96.0.0/12"},
		DNSServer:    "10.96.0.10",
	}, Options{
		BridgePort: 17890, ControllerPort: 19090, ControllerSecret: "test-secret",
		DNSPort: 1053, Namespace: "default",
		Hosts: []sessionspec.HostAlias{{Domain: "app.dev", IP: "10.96.0.50"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, item := range []string{`"type": "hosts"`, `"app.dev"`, `"10.96.0.50"`} {
		if !strings.Contains(text, item) {
			t.Fatalf("generated config missing %q:\n%s", item, text)
		}
	}
}

func TestNormalizeHostAliasesClearsEmpty(t *testing.T) {
	got, err := NormalizeHostAliases(nil)
	if err != nil || got != nil {
		t.Fatalf("empty aliases = %v, %v", got, err)
	}
}

func TestSearchDomains(t *testing.T) {
	got := SearchDomains("demo")
	want := []string{"demo.svc.cluster.local", "svc.cluster.local", "cluster.local"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("SearchDomains = %v, want %v", got, want)
	}
	if strings.Join(SearchDomains(""), ",") != strings.Join(SearchDomains("default"), ",") {
		t.Fatal("empty namespace should default to default")
	}

	all := SearchDomainsForNamespaces([]string{"demo", "default", "demo"})
	allWant := []string{
		"demo.svc.cluster.local",
		"default.svc.cluster.local",
		"svc.cluster.local",
		"cluster.local",
	}
	if strings.Join(all, ",") != strings.Join(allWant, ",") {
		t.Fatalf("SearchDomainsForNamespaces = %v, want %v", all, allWant)
	}
}
