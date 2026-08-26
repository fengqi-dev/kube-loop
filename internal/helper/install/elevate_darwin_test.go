//go:build darwin

package install

import (
	"strings"
	"testing"
)

func TestDarwinUninstallCommandCombinesCertificateAndHelper(t *testing.T) {
	command := darwinUninstallCommand(
		"/Applications/Kube Loop/helper", "/tmp/inspection-ca.pem",
	)
	if !strings.Contains(command, darwinTrustCertificateCommandName) ||
		!strings.Contains(command, "--operation uninstall") ||
		!strings.Contains(command, "/tmp/inspection-ca.pem") {
		t.Fatalf("combined uninstall command = %q", command)
	}
	if strings.Count(command, darwinTrustCertificateCommandName) != 1 {
		t.Fatalf("certificate operation must run once, command = %q", command)
	}
	if !strings.HasSuffix(command, "uninstall") {
		t.Fatalf("helper uninstall must run last, command = %q", command)
	}
}

func TestDarwinTrustCertificateCommandUsesHiddenHelperOperation(t *testing.T) {
	command := darwinTrustCertificateCommand(
		"/Applications/Kube Loop/helper", "/tmp/inspection-ca.pem",
	)
	if !strings.Contains(command, darwinTrustCertificateCommandName) ||
		!strings.Contains(command, "--operation install") ||
		!strings.Contains(command, "/tmp/inspection-ca.pem") ||
		strings.Contains(command, "administrator privileges") {
		t.Fatalf("trust certificate command = %q", command)
	}
}

func TestDarwinUninstallCommandOmitsMissingCertificate(t *testing.T) {
	command := darwinUninstallCommand("/Applications/Kube Loop/helper", "")
	if strings.Contains(command, "security") || strings.Contains(command, "\n") {
		t.Fatalf("helper-only uninstall command = %q", command)
	}
	if !strings.Contains(command, "uninstall") {
		t.Fatalf("helper-only uninstall command = %q", command)
	}
}
