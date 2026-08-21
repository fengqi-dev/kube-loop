package componentstore

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirectoryForUsesVersionedCacheLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	tests := []struct {
		release string
		root    string
	}{
		{release: "v2.1.0", root: ".kubeloop"},
		{release: "dev", root: ".kubeloop-dev"},
	}
	for _, test := range tests {
		t.Run(test.release, func(t *testing.T) {
			path, err := directoryFor(test.release)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(home, test.root, "cache", "components", test.release, runtime.GOOS+"-"+runtime.GOARCH)
			if path != want {
				t.Fatalf("directoryFor() = %q, want %q", path, want)
			}
		})
	}
}
