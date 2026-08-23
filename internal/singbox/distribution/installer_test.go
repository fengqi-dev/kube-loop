package distribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerEnsureUsesOverride(t *testing.T) {
	override := writeTestExecutable(t, "override-sing-box", []byte("override"))
	t.Setenv("KUBELOOP_SINGBOX_PATH", override)

	path, err := (&Installer{DisableDownload: true}).Ensure(t.Context())
	if err != nil || path != override {
		t.Fatalf("Ensure() path = %q, error = %v", path, err)
	}
}

func TestInstallerEnsureUsesBundledBinary(t *testing.T) {
	t.Parallel()

	bundled := writeTestExecutable(t, "bundled-sing-box", []byte("bundled"))
	path, err := (&Installer{
		BundledPath: bundled, DisableOverride: true, DisableDownload: true,
	}).Ensure(t.Context())
	if err != nil || path != bundled {
		t.Fatalf("Ensure() path = %q, error = %v", path, err)
	}
}

func TestInstallerEnsureReportsMissingBundledBinary(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing-sing-box")
	_, err := (&Installer{
		BundledPath: missing, DisableOverride: true, DisableDownload: true,
	}).Ensure(t.Context())
	if err == nil || !strings.Contains(err.Error(), missing) ||
		!strings.Contains(err.Error(), "reinstall the KubeLoop package") {
		t.Fatalf("Ensure() error = %v", err)
	}
}

func writeTestExecutable(t *testing.T, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
