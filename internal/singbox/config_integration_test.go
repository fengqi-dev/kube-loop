package singbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

func TestGeneratedConfigAcceptedBySingBox(t *testing.T) {
	binary := os.Getenv("KUBELOOP_SINGBOX_PATH")
	if binary == "" {
		t.Skip("KUBELOOP_SINGBOX_PATH is not set")
	}
	content, err := Generate(NetworkSpec{
		PodCIDRs:     []string{"10.244.0.0/16"},
		ServiceCIDRs: []string{"10.96.0.0/12"},
		DNSServer:    "10.96.0.10",
	}, Options{
		BridgePort: 17890, ControllerPort: 19090,
		ControllerSecret: "controller-secret-1234567890123456",
		DNSPort:          1053, TUNAddress: "198.19.0.1/30",
		TrafficPorts:    sessionspec.TrafficInboundPorts{Listen: 18081},
		TrafficPassword: "traffic-password-1234567890123456",
	})
	if err != nil {
		t.Fatal(err)
	}
	checkSingBoxConfig(t, binary, "base", content)
}

func checkSingBoxConfig(t *testing.T, binary, name string, content []byte) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config-"+name+".json")
	if err := os.WriteFile(configPath, content, 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "check", "-c", configPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("sing-box config check (%s) failed: %v\n%s", name, err, output)
	}
}
