package config

import "testing"

func TestDefaultConfigSharesTunnelPath(t *testing.T) {
	t.Parallel()

	config := defaultConfig()
	if config.HTTP.Path != "/tunnel" || config.Forward.Path != config.HTTP.Path {
		t.Fatalf("tunnel paths = control %q, forward %q", config.HTTP.Path, config.Forward.Path)
	}
	config.Relay.ControlPlaneURL = "https://control-plane.example.test"
	config.Relay.Endpoint = "wss://gateway.example.test/tunnel"
	if err := config.normalizeAndValidate(); err != nil {
		t.Fatalf("shared tunnel path rejected: %v", err)
	}
}
