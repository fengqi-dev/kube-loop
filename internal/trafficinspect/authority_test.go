package trafficinspect

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrCreateAuthority_ReusesDeviceIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", authorityFileName)
	first, err := LoadOrCreateAuthority(path)
	if err != nil {
		t.Fatalf("create authority: %v", err)
	}
	second, err := LoadOrCreateAuthority(path)
	if err != nil {
		t.Fatalf("reload authority: %v", err)
	}
	if second.FingerprintSHA256() != first.FingerprintSHA256() {
		t.Fatalf("reloaded fingerprint = %q, want %q", second.FingerprintSHA256(), first.FingerprintSHA256())
	}
	if len(first.TLSCertificate().Certificate) != 1 {
		t.Fatalf("certificate chain length = %d, want 1", len(first.TLSCertificate().Certificate))
	}
	block, trailing := pem.Decode(first.PublicCertificatePEM())
	if block == nil || block.Type != pemCertificateType || len(trailing) != 0 {
		t.Fatal("public certificate output is not one certificate PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse public certificate: %v", err)
	}
	if !certificate.IsCA || certificate.Subject.CommonName != AuthorityCommonName {
		t.Fatalf("unexpected authority certificate: is_ca=%t common_name=%q", certificate.IsCA, certificate.Subject.CommonName)
	}
	fingerprint := sha256.Sum256(certificate.Raw)
	if got := strings.ToUpper(hex.EncodeToString(fingerprint[:])); got != first.FingerprintSHA256() {
		t.Fatalf("fingerprint = %q, want %q", got, first.FingerprintSHA256())
	}
	if strings.Contains(string(first.PublicCertificatePEM()), "PRIVATE KEY") {
		t.Fatal("public certificate output contains the private key")
	}
	if runtime.GOOS != goosWindows {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat authority: %v", statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("authority permissions = %04o, want 0600", info.Mode().Perm())
		}
	}
}

func TestLoadOrCreateAuthority_RejectsUnsafeExistingState(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
		want    string
	}{
		{
			name: "corrupt bundle",
			prepare: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
					t.Fatalf("write corrupt authority: %v", err)
				}
			},
			want: "certificate PEM is invalid",
		},
		{
			name: "symbolic link",
			prepare: func(t *testing.T, path string) {
				t.Helper()
				target := path + ".target"
				if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
					t.Fatalf("write symlink target: %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("create authority symlink: %v", err)
				}
			},
			want: "must be a regular file",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), authorityFileName)
			test.prepare(t, path)
			_, err := LoadOrCreateAuthority(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadOrCreateAuthority() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestLoadOrCreateAuthority_RejectsLoosePermissions(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("Windows does not expose Unix file permission bits")
	}
	path := filepath.Join(t.TempDir(), authorityFileName)
	if _, err := LoadOrCreateAuthority(path); err != nil {
		t.Fatalf("create authority: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("loosen authority permissions: %v", err)
	}
	_, err := LoadOrCreateAuthority(path)
	if err == nil || !strings.Contains(err.Error(), "want 0600") {
		t.Fatalf("LoadOrCreateAuthority() error = %v, want permission rejection", err)
	}
}

func TestLoadOrCreateAuthority_RejectsRelativePath(t *testing.T) {
	_, err := LoadOrCreateAuthority(authorityFileName)
	if err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("LoadOrCreateAuthority() error = %v, want absolute path rejection", err)
	}
}
