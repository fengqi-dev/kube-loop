package main

import (
	"os"
	"path/filepath"
	"testing"
)

type fakeRuntimeGateway struct {
	draining bool
	active   int
}

func (state fakeRuntimeGateway) Draining() bool         { return state.draining }
func (state fakeRuntimeGateway) ActiveConnections() int { return state.active }

type fakeRelayReadiness struct{ ready bool }

func (state fakeRelayReadiness) Ready() bool { return state.ready }

func TestLoadGatewayConfigAppliesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	raw := []byte(`{"relay":{"controlPlaneURL":"https://registry.example.test","endpoint":"wss://relay.example.test/tunnel"}}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := loadGatewayConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.HTTP.Listen != ":8080" || config.HTTP.Path != "/v1/tunnel" || config.WebSocket.MaxSessions != 256 {
		t.Fatalf("Gateway defaults = %#v", config)
	}
}

func TestLoadGatewayConfigRejectsLegacyRelayFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.json")
	raw := []byte(`{"relay":{"controlPlaneURL":"https://registry.example.test","endpoint":"wss://relay.example.test/tunnel","id":"legacy"}}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGatewayConfig(path); err == nil {
		t.Fatal("legacy Relay ID was accepted")
	}
}

func TestExpandRelayEndpointUsesDownwardAPIIdentity(t *testing.T) {
	endpoint, err := expandRelayEndpoint(
		"wss://{podName}.relay.example/tunnel/{podUID}",
		gatewayEnvironment{PodName: "gateway-7", PodUID: "44444444-4444-4444-8444-444444444444"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "wss://gateway-7.relay.example/tunnel/44444444-4444-4444-8444-444444444444" {
		t.Fatalf("endpoint = %q", endpoint)
	}
}

func TestLoadGatewayEnvironment(t *testing.T) {
	t.Setenv("KUBELOOP_GATEWAY_CONFIG_FILE", " /etc/kubeloop/gateway.json ")
	t.Setenv("KUBELOOP_POD_NAME", " gateway-7 ")
	t.Setenv("KUBELOOP_POD_UID", " pod-uid ")
	t.Setenv("KUBELOOP_POD_IP", " 10.0.0.7 ")

	environment, err := loadGatewayEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if environment.ConfigFile != "/etc/kubeloop/gateway.json" || environment.PodName != "gateway-7" ||
		environment.PodUID != "pod-uid" || environment.PodIP != "10.0.0.7" {
		t.Fatalf("Gateway environment = %#v", environment)
	}
}

func TestLoadGatewayEnvironmentRequiresConfigFile(t *testing.T) {
	t.Setenv("KUBELOOP_GATEWAY_CONFIG_FILE", "")

	if _, err := loadGatewayEnvironment(); err == nil {
		t.Fatal("missing Gateway configuration file was accepted")
	}
}

func TestOperationsGatewayReadinessRequiresCurrentRelayLease(t *testing.T) {
	state := operationsGatewayState{
		gateway: fakeRuntimeGateway{active: 4},
		agent:   fakeRelayReadiness{ready: false},
	}
	if state.Ready() {
		t.Fatal("gateway reported ready before Relay registration and lease became current")
	}
	if state.Draining() {
		t.Fatal("an unavailable Relay dependency must not be reported as an active drain")
	}
	if state.ActiveConnections() != 4 {
		t.Fatalf("active connections = %d", state.ActiveConnections())
	}

	state.agent = fakeRelayReadiness{ready: true}
	if !state.Ready() {
		t.Fatal("gateway did not become ready after Relay registration and lease became current")
	}

	state.gateway = fakeRuntimeGateway{draining: true, active: 4}
	if state.Ready() || !state.Draining() {
		t.Fatal("draining gateway reported ready")
	}
}

func TestOperationsGatewayWithoutRegistryUsesRuntimeState(t *testing.T) {
	state := operationsGatewayState{gateway: fakeRuntimeGateway{}}
	if !state.Ready() {
		t.Fatal("gateway without Relay Registry did not use its local runtime readiness")
	}

	zero := operationsGatewayState{}
	if zero.Ready() || zero.Draining() || zero.ActiveConnections() != 0 {
		t.Fatal("zero operations state must remain unavailable without panicking")
	}
}
