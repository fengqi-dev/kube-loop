//go:build darwin

package install

import (
	"strings"
	"testing"
)

func TestDarwinSupervisorInstallCommandCombinesCertificate(t *testing.T) {
	t.Parallel()
	command := darwinSupervisorInstallCommand(
		"/tmp/supervisor", "supervisor-sha", "/tmp/worker", "worker-sha",
		"v2.1.0", "release", "token", 501, "/Users/test", "/Applications/KubeLoop.app/Contents/Resources/sing-box",
		"/tmp/inspection-ca.pem",
	)
	if strings.Count(command, "security add-trusted-cert") != 1 {
		t.Fatalf("combined install command = %q", command)
	}
	if !strings.Contains(command, "kubeloop-supervisor") ||
		!strings.Contains(command, "--worker-version v2.1.0") ||
		!strings.Contains(command, "--channel release") ||
		!strings.Contains(command, "/tmp/inspection-ca.pem") ||
		!strings.Contains(command, "/Library/Keychains/System.keychain") {
		t.Fatalf("combined install command = %q", command)
	}
}

func TestDarwinSupervisorInstallCommandOmitsMissingCertificate(t *testing.T) {
	t.Parallel()
	command := darwinSupervisorInstallCommand(
		"/tmp/supervisor", "supervisor-sha", "/tmp/worker", "worker-sha",
		"dev", "dev", "token", 501, "/Users/test", "/tmp/sing-box", "",
	)
	if strings.Contains(command, "security add-trusted-cert") {
		t.Fatalf("helper-only install command = %q", command)
	}
}
