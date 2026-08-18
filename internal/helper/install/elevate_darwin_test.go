//go:build darwin

package install

import (
	"strings"
	"testing"
)

func TestDarwinUninstallCommandCombinesCertificateAndHelper(t *testing.T) {
	command := darwinUninstallCommand("/Applications/Kube Loop/helper", "ABCDEF0123")
	if !strings.Contains(command, "security delete-certificate") ||
		!strings.Contains(command, "ABCDEF0123") ||
		!strings.Contains(command, "uninstall") {
		t.Fatalf("combined uninstall command = %q", command)
	}
	if strings.Count(command, "\n") != 1 {
		t.Fatalf("combined uninstall command = %q", command)
	}
}

func TestDarwinUninstallCommandOmitsMissingCertificate(t *testing.T) {
	command := darwinUninstallCommand("/Applications/Kube Loop/helper", "")
	if strings.Contains(command, "security") || !strings.Contains(command, "uninstall") {
		t.Fatalf("helper-only uninstall command = %q", command)
	}
}
