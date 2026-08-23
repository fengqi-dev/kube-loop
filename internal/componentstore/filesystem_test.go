package componentstore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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

func TestReadManifestRejectsUnknownFieldsAndVersions(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "manifest.json")
	for _, test := range []struct {
		name string
		raw  string
	}{
		{name: "unknown field", raw: `{"version":1,"release":"dev","platform":"test","components":{},"extra":true}`},
		{name: "unsupported version", raw: `{"version":2,"release":"dev","platform":"test","components":{}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readManifest(directory); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func TestAcquireReleaseLockRecoversStaleDirectory(t *testing.T) {
	directory := t.TempDir()
	lockPath := filepath.Join(directory, ".lock")
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lockPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireReleaseLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, err := os.Stat(lockPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release lock still exists: %v", err)
	}
}
