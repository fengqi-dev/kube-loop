//go:build darwin

package install

import (
	"strings"
	"testing"
)

func TestDarwinSupervisorInstallCommandLeavesCertificateTrustToApplication(t *testing.T) {
	t.Parallel()
	command := darwinSupervisorInstallCommand(
		"/tmp/supervisor", "supervisor-sha", "/tmp/worker", "worker-sha",
		"v2.1.0", "release", "token", 501, "/Users/test", "/Applications/KubeLoop.app/Contents/Resources/sing-box",
	)
	if strings.Contains(command, "security add-trusted-cert") || strings.Contains(command, "System.keychain") {
		t.Fatalf("supervisor install command changes certificate trust: %q", command)
	}
	if !strings.Contains(command, "kubeloop-supervisor") ||
		!strings.Contains(command, "--worker-version v2.1.0") ||
		!strings.Contains(command, "--channel release") {
		t.Fatalf("combined install command = %q", command)
	}
}
