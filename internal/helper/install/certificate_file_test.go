package install

import (
	"os"
	"strings"
	"testing"
)

func TestWriteTemporaryPublicCertificateRejectsPrivateMaterial(t *testing.T) {
	t.Parallel()
	_, _, err := writeTemporaryPublicCertificate([]byte(
		"-----BEGIN CERTIFICATE-----\nY2VydA==\n-----END CERTIFICATE-----\n" +
			"-----BEGIN PRIVATE KEY-----\na2V5\n-----END PRIVATE KEY-----\n",
	))
	if err == nil || !strings.Contains(err.Error(), "PEM is invalid") {
		t.Fatalf("writeTemporaryPublicCertificate() error = %v", err)
	}
}

func TestWriteTemporaryPublicCertificateCleansUp(t *testing.T) {
	t.Parallel()
	path, cleanup, err := writeTemporaryPublicCertificate([]byte(
		"-----BEGIN CERTIFICATE-----\nY2VydA==\n-----END CERTIFICATE-----\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("temporary certificate mode: info=%v err=%v", info, err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temporary certificate remains: %v", err)
	}
}
