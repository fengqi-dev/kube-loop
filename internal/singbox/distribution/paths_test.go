package distribution

import (
	"path/filepath"
	"testing"
)

func TestDefaultBaseDirUsesCacheDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := (&Installer{}).baseDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".kubeloop", "cache"); path != want {
		t.Fatalf("baseDir() = %q, want %q", path, want)
	}
}
