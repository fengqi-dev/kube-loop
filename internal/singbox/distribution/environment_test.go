package distribution

import "testing"

func TestLoadInstallerEnvironment(t *testing.T) {
	t.Setenv("KUBELOOP_SINGBOX_PATH", " /opt/kubeloop/sing-box ")

	environment, err := loadInstallerEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	if environment.SingBoxPath != "/opt/kubeloop/sing-box" {
		t.Fatalf("sing-box path = %q", environment.SingBoxPath)
	}
}
