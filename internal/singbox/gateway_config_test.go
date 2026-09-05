package singbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateGatewaySessionConfigUsesTrojanAndRejectsByDefault(t *testing.T) {
	t.Parallel()

	raw, err := GenerateGatewaySessionConfig(GatewaySessionOptions{
		SessionID: "session-1", ListenPort: 19090,
		TrojanPassword: strings.Repeat("a", 64),
		Network: NetworkSpec{
			PodCIDRs:       []string{"10.244.0.0/16"},
			ServiceCIDRs:   []string{"10.96.0.0/12"},
			DNSServer:      "10.96.0.10",
			ClusterDomains: []string{"cluster.local"},
		},
		LogLevel: "debug",
	})
	if err != nil {
		t.Fatalf("GenerateGatewaySessionConfig() error = %v", err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	inbounds := config["inbounds"].([]any)
	inbound := inbounds[0].(map[string]any)
	if inbound["type"] != "trojan" || inbound["listen"] != "127.0.0.1" || inbound["listen_port"] != float64(19090) {
		t.Fatalf("unexpected Trojan inbound: %#v", inbound)
	}
	transport := inbound["transport"].(map[string]any)
	if transport["type"] != "ws" || transport["path"] != GatewayWebSocketPath {
		t.Fatalf("unexpected Trojan WebSocket transport: %#v", transport)
	}
	route := config["route"].(map[string]any)
	rules := route["rules"].([]any)
	resolveRule := rules[0].(map[string]any)
	if resolveRule[configActionKey] != "resolve" || resolveRule[configServerKey] != clusterDNSServer {
		t.Fatalf("cluster domain resolve rule = %#v", resolveRule)
	}
	ipRule := rules[1].(map[string]any)
	if _, ok := ipRule[configIPCIDRKey]; !ok {
		t.Fatalf("cluster IP rule is missing: %#v", ipRule)
	}
	last := rules[len(rules)-1].(map[string]any)
	if last["action"] != "reject" {
		t.Fatalf("final Session rule = %#v, want reject", last)
	}
	if route["default_domain_resolver"] != "kubeloop-cluster-dns" {
		t.Fatalf("default domain resolver = %#v", route["default_domain_resolver"])
	}
	dnsConfig := config["dns"].(map[string]any)
	dnsServers := dnsConfig["servers"].([]any)
	dnsServer := dnsServers[0].(map[string]any)
	if dnsServer["server"] != "10.96.0.10" {
		t.Fatalf("unexpected Gateway cluster DNS server: %#v", dnsServer)
	}
	text := string(raw)
	for _, expected := range []string{"10.244.0.0/16", "10.96.0.0/12", "10.96.0.10", "cluster.local"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("config does not contain %q:\n%s", expected, text)
		}
	}
}

func TestGenerateGatewaySessionConfigRejectsInvalidCredentialsAndNetwork(t *testing.T) {
	t.Parallel()

	valid := GatewaySessionOptions{
		SessionID: "session-1", ListenPort: 19090,
		TrojanPassword: strings.Repeat("a", 64),
		Network:        NetworkSpec{ServiceIPs: []string{"10.96.0.10"}},
	}
	tests := map[string]func(*GatewaySessionOptions){
		"session":  func(options *GatewaySessionOptions) { options.SessionID = "../session" },
		"port":     func(options *GatewaySessionOptions) { options.ListenPort = 0 },
		"password": func(options *GatewaySessionOptions) { options.TrojanPassword = strings.Repeat("z", 64) },
		"network":  func(options *GatewaySessionOptions) { options.Network.ServiceIPs = []string{"not-an-ip"} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			options := valid
			mutate(&options)
			if _, err := GenerateGatewaySessionConfig(options); err == nil {
				t.Fatal("GenerateGatewaySessionConfig() error = nil")
			}
		})
	}
}
