package distribution

import "testing"

func TestLoadInstallerEnvironment(t *testing.T) {
	t.Setenv("KUBELOOP_SINGBOX_PATH", " /opt/kubeloop/sing-box ")

	environment := loadInstallerEnvironment()
	if environment.SingBoxPath != "/opt/kubeloop/sing-box" {
		t.Fatalf("sing-box path = %q", environment.SingBoxPath)
	}
}
