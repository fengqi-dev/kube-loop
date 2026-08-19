//go:build linux

package install

import (
	"encoding/pem"
	"os"
	"strings"
	"testing"
)

func TestWriteTemporaryLinuxCertificateValidatesAndCleansUp(t *testing.T) {
	if _, _, err := writeTemporaryLinuxCertificate([]byte("not a certificate")); err == nil {
		t.Fatal("invalid certificate was accepted")
	}
	content := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("certificate")})
	path, cleanup, err := writeTemporaryLinuxCertificate(content)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("temporary certificate mode = %o, want 600", info.Mode().Perm())
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary certificate remains after cleanup: %v", err)
	}
}

func TestLinuxInstallScriptCombinesHelperAndSystemTrustInstallation(t *testing.T) {
	helperIndex := strings.Index(linuxInstallScript, `"$staged" install`)
	certificateIndex := strings.Index(linuxInstallScript, `if [ -n "$8" ]`)
	if helperIndex < 0 || certificateIndex <= helperIndex {
		t.Fatalf("Linux install script does not combine helper and certificate installation:\n%s", linuxInstallScript)
	}
	for _, expected := range []string{
		"update-ca-certificates",
		"/usr/local/share/ca-certificates/kubeloop-traffic-inspection.crt",
		`"$update_command" extract`,
		"/etc/pki/ca-trust/source/anchors/kubeloop-traffic-inspection.pem",
	} {
		if !strings.Contains(linuxInstallScript, expected) {
			t.Fatalf("Linux install script is missing %q", expected)
		}
	}
}
