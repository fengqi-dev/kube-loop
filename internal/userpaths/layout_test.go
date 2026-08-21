package userpaths

import (
	"path/filepath"
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
