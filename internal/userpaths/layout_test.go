package userpaths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestForVersionSeparatesReleaseAndDevelopment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	tests := []struct {
		name    string
		version string
		root    string
	}{
		{name: "release", version: "v2.1.0", root: ".kubeloop"},
		{name: "development", version: "dev", root: ".kubeloop-dev"},
		{name: "empty development version", root: ".kubeloop-dev"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout, err := ForVersion(test.version)
			if err != nil {
				t.Fatal(err)
			}
			wantRoot := filepath.Join(home, test.root)
			if layout.Root() != wantRoot {
				t.Fatalf("Root() = %q, want %q", layout.Root(), wantRoot)
			}
			if layout.ConfigDir() != filepath.Join(wantRoot, "config") ||
				layout.DataDir() != filepath.Join(wantRoot, "data") ||
				layout.StateDir() != filepath.Join(wantRoot, "state") ||
				layout.SecretsDir() != filepath.Join(wantRoot, "secrets") ||
				layout.CacheDir() != filepath.Join(wantRoot, "cache") {
				t.Fatalf("layout directories are not rooted at %q", wantRoot)
			}
		})
	}
}

func TestNewRequiresRoot(t *testing.T) {
	t.Parallel()
	if _, err := New(" "); err == nil {
		t.Fatal("New() accepted an empty root")
	}
}

func TestEnsureCreatesPrivateDirectoryTree(t *testing.T) {
	t.Parallel()
	layout, err := New(filepath.Join(t.TempDir(), "kubeloop"))
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{
		layout.Root(), layout.ConfigDir(), layout.DataDir(), layout.StateDir(), layout.SecretsDir(), layout.CacheDir(),
	} {
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			t.Fatalf("directory %q was not created: %v", directory, err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
			t.Fatalf("directory %q mode = %o, want 700", directory, info.Mode().Perm())
		}
	}
}
