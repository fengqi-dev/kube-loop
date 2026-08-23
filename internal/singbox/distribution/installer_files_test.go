package distribution

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteExecutableReplacesDestination(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "sing-box")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeExecutable(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "new" {
		t.Fatalf("installed content = %q, error = %v", content, err)
	}
	matches, err := filepath.Glob(filepath.Join(directory, ".sing-box-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files = %v, error = %v", matches, err)
	}
}

func TestInstallerPlatformOverride(t *testing.T) {
	t.Parallel()

	goos, goarch := (&Installer{GOOS: "linux", GOARCH: "arm64"}).platform()
	if goos != "linux" || goarch != "arm64" {
		t.Fatalf("platform = %s/%s", goos, goarch)
	}
}

func TestValidateBinaryRejectsDirectory(t *testing.T) {
	t.Parallel()

	if _, err := validateBinary(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("validateBinary() error = %v", err)
	}
}
