package store

import (
	"path/filepath"
	"testing"
)

func TestGatewayTransportDefaultsAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	stateStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := stateStore.GatewayTransport("minikube").Mode; mode != "port-forward" {
		t.Fatalf("default Gateway mode = %q", mode)
	}
	want := GatewayTransport{
		Mode: "websocket", URL: "wss://gateway.example.com/v1/tunnel", Token: "secret",
		Exposure: "gateway-api", GatewayNamespace: "gateway-system",
		GatewayName: "shared", GatewaySection: "https",
		PoolSize: 2, MaxPhysical: 4, MaxStreams: 128,
	}
	if err := stateStore.SetGatewayTransport("minikube", want); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GatewayTransport("minikube"); got != want {
		t.Fatalf("Gateway transport = %+v, want %+v", got, want)
	}
}
