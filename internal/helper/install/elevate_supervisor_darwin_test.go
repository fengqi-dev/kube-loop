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
		"",
	)
	if strings.Contains(command, darwinTrustCertificateCommandName) || strings.Contains(command, "System.keychain") {
		t.Fatalf("supervisor install command changes certificate trust: %q", command)
	}
	if !strings.Contains(command, "kubeloop-supervisor") ||
		!strings.Contains(command, "--worker-version v2.1.0") ||
		!strings.Contains(command, "--channel release") {
		t.Fatalf("combined install command = %q", command)
	}
}

func TestDarwinSupervisorInstallCommandCombinesCertificateTrust(t *testing.T) {
	command := darwinSupervisorInstallCommand(
		"/tmp/supervisor", "supervisor-sha", "/tmp/worker", "worker-sha",
		"1.2.3", "release", "token", 501, "/Users/test", "/tmp/sing-box",
		"/tmp/inspection-ca.pem",
	)
	if !strings.Contains(command, `"$worker" trust-certificate`) ||
		!strings.Contains(command, "--operation install") ||
		!strings.Contains(command, "/tmp/inspection-ca.pem") {
		t.Fatalf("combined install command = %q", command)
	}
}
