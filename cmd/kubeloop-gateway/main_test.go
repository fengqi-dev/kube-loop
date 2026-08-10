package main

import (
	"testing"
	"time"
)

type fakeRuntimeGateway struct {
	draining bool
	active   int
}

func (state fakeRuntimeGateway) Draining() bool         { return state.draining }
func (state fakeRuntimeGateway) ActiveConnections() int { return state.active }

type fakeRelayReadiness struct{ ready bool }

func (state fakeRelayReadiness) Ready() bool { return state.ready }

func TestGatewayEnvironmentDefaults(t *testing.T) {
	t.Setenv("KUBELOOP_GATEWAY_HTTP_PATH", " /tunnel ")
	if got := stringEnv("KUBELOOP_GATEWAY_HTTP_PATH", "/fallback"); got != "/tunnel" {
		t.Fatalf("HTTP path = %q", got)
	}
	t.Setenv("KUBELOOP_GATEWAY_DRAIN_TIMEOUT", "45s")
	if got, err := durationEnv("KUBELOOP_GATEWAY_DRAIN_TIMEOUT", time.Second); err != nil || got != 45*time.Second {
		t.Fatalf("drain timeout = %v, error = %v", got, err)
	}
	t.Setenv("KUBELOOP_GATEWAY_STREAM_IDLE_TIMEOUT", "15m")
	if got, err := durationEnv("KUBELOOP_GATEWAY_STREAM_IDLE_TIMEOUT", time.Second); err != nil || got != 15*time.Minute {
		t.Fatalf("stream idle timeout = %v, error = %v", got, err)
	}
}

func TestGatewayRejectsInvalidDurationEnvironment(t *testing.T) {
	for _, name := range []string{"KUBELOOP_GATEWAY_DRAIN_TIMEOUT", "KUBELOOP_GATEWAY_STREAM_IDLE_TIMEOUT"} {
		t.Run(name, func(t *testing.T) {
			for _, value := range []string{"invalid", "0s", "-1s"} {
				t.Run(value, func(t *testing.T) {
					t.Setenv(name, value)
					if _, err := durationEnv(name, time.Second); err == nil {
						t.Fatalf("durationEnv accepted %q", value)
					}
				})
			}
		})
	}
}

func TestExpandRelayEndpointUsesDownwardAPIIdentity(t *testing.T) {
	t.Setenv("KUBELOOP_POD_NAME", "gateway-7")
	t.Setenv("KUBELOOP_POD_UID", "44444444-4444-4444-8444-444444444444")
	endpoint, err := expandRelayEndpoint("wss://{podName}.relay.example/tunnel/{podUID}")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "wss://gateway-7.relay.example/tunnel/44444444-4444-4444-8444-444444444444" {
		t.Fatalf("endpoint = %q", endpoint)
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
