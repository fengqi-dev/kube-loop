package config

import "testing"

func TestLoadControlPlaneEnvironment(t *testing.T) {
	t.Setenv("KUBELOOP_POD_NAME", " control-plane-0 ")

	environment := LoadEnvironmentFrom(NewConfigResolver())
	if environment.PodName != "control-plane-0" {
		t.Fatalf("Control Plane Pod name = %q", environment.PodName)
	}
}
