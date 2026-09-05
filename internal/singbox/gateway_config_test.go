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
	last := rules[len(rules)-1].(map[string]any)
	if last["action"] != "reject" {
		t.Fatalf("final Session rule = %#v, want reject", last)
	}
	text := string(raw)
	for _, expected := range []string{"10.244.0.0/16", "10.96.0.0/12", "cluster.local"} {
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
