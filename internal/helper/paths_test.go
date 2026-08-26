package helper

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSeparateReleaseAndDevelopment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	previous := Version
	t.Cleanup(func() { Version = previous })

	Version = "v2.1.0"
	releaseTokenPath, err := TokenPath()
	if err != nil || releaseTokenPath != filepath.Join(home, ".kubeloop", "secrets", "helper.token") {
		path := releaseTokenPath
		t.Fatalf("release token path = %q, err = %v", path, err)
	}
	if _, err := EnsureUserToken(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Dir(releaseTokenPath))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("secrets directory mode = %v", info.Mode().Perm())
		}
		info, err = os.Stat(releaseTokenPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("helper token mode = %v", info.Mode().Perm())
		}
	}
	Version = "dev"
	if path, err := TokenPath(); err != nil || path != filepath.Join(home, ".kubeloop-dev", "secrets", "helper.token") {
		t.Fatalf("development token path = %q, err = %v", path, err)
	}
}
