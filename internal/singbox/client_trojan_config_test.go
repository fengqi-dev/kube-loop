package singbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateClientTrojanConfigUsesSingleWebSocketConnection(t *testing.T) {
	t.Parallel()

	raw, err := GenerateClientTrojanConfig(ClientTrojanOptions{
		SessionID: "session-1", ListenPort: 1080,
		Endpoint:    "wss://gateway.example.test:8443/tunnel",
		RelayTicket: "header.payload.signature", TrojanPassword: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	outbound := config["outbounds"].([]any)[0].(map[string]any)
	if outbound["type"] != "trojan" || outbound["server"] != "gateway.example.test" ||
		outbound["server_port"] != float64(8443) {
		t.Fatalf("unexpected Trojan outbound: %#v", outbound)
	}
	transport := outbound["transport"].(map[string]any)
	headers := transport["headers"].(map[string]any)
	if transport["type"] != "ws" || transport["path"] != "/tunnel" ||
		headers["Authorization"] != "Bearer header.payload.signature" {
		t.Fatalf("unexpected WebSocket transport: %#v", transport)
	}
	multiplex := outbound["multiplex"].(map[string]any)
	if multiplex["max_connections"] != float64(1) || multiplex["enabled"] != true {
		t.Fatalf("unexpected multiplex config: %#v", multiplex)
	}
}

func TestGenerateClientTrojanConfigRejectsUnsafeInput(t *testing.T) {
	t.Parallel()

	valid := ClientTrojanOptions{
		SessionID: "session-1", ListenPort: 1080, Endpoint: "ws://127.0.0.1:8080/tunnel",
		RelayTicket: "ticket", TrojanPassword: strings.Repeat("a", 64),
	}
	tests := map[string]func(*ClientTrojanOptions){
		"session":  func(options *ClientTrojanOptions) { options.SessionID = "../bad" },
		"port":     func(options *ClientTrojanOptions) { options.ListenPort = 0 },
		"scheme":   func(options *ClientTrojanOptions) { options.Endpoint = "http://gateway/tunnel" },
		"query":    func(options *ClientTrojanOptions) { options.Endpoint += "?token=bad" },
		"ticket":   func(options *ClientTrojanOptions) { options.RelayTicket = "bad\nticket" },
		"password": func(options *ClientTrojanOptions) { options.TrojanPassword = strings.Repeat("z", 64) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			options := valid
			mutate(&options)
			if _, err := GenerateClientTrojanConfig(options); err == nil {
				t.Fatal("GenerateClientTrojanConfig() error = nil")
			}
		})
	}
}
