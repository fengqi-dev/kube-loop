package cli

import "testing"

func TestNewViperMapsNestedHyphenatedKeysToEnvironment(t *testing.T) {
	t.Setenv("KUBELOOP_GATEWAY_CONFIG_FILE", "/tmp/gateway.yaml")
	first := NewViper()
	if got := first.GetString("gateway.config-file"); got != "/tmp/gateway.yaml" {
		t.Fatalf("gateway config file = %q", got)
	}

	t.Setenv("KUBELOOP_GATEWAY_CONFIG_FILE", "/tmp/other.yaml")
	second := NewViper()
	if got := second.GetString("gateway.config-file"); got != "/tmp/other.yaml" {
		t.Fatalf("isolated gateway config file = %q", got)
	}
}
