package componentstore

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestCacheAndFindVerifyManifestContentAndPermissions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	source := filepath.Join(t.TempDir(), "kubeloop-helper")
	contents := []byte("verified component payload")
	if err := os.WriteFile(source, contents, 0o700); err != nil {
		t.Fatal(err)
	}

	cached, err := Cache("dev", "kubeloop-helper", source)
	if err != nil {
		t.Fatal(err)
	}
	found, err := Find("dev", "kubeloop-helper")
	if err != nil || found != cached {
		t.Fatalf("Find() = %q, %v", found, err)
	}
	stored, err := os.ReadFile(found)
	if err != nil || string(stored) != string(contents) {
		t.Fatalf("cached contents = %q, error = %v", stored, err)
	}
	current, err := readManifest(filepath.Dir(cached))
	if err != nil {
		t.Fatal(err)
	}
	entry := current.Components["kubeloop-helper"]
	if current.Version != manifestVersion || current.Release != "dev" ||
		entry.Size != int64(len(contents)) || entry.SHA256 == "" {
		t.Fatalf("manifest = %#v", current)
	}
	if same, err := Cache("dev", "kubeloop-helper", cached); err != nil || same != cached {
		t.Fatalf("same-path Cache() = %q, %v", same, err)
	}

	if err := os.WriteFile(cached, []byte("tampered component payload"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Find("dev", "kubeloop-helper"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered Find() error = %v", err)
	}
	if _, err := Cache("dev", "kubeloop-helper", source); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(cached, 0o744); err != nil {
			t.Fatal(err)
		}
		if _, err := Find("dev", "kubeloop-helper"); err == nil || !strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("unsafe permission Find() error = %v", err)
		}
	}
}

func TestCacheRejectsUnsafeSegmentsAndSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	source := filepath.Join(t.TempDir(), "component")
	if err := os.WriteFile(source, []byte("component"), 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		release string
		item    string
		source  string
	}{
		{name: "release traversal", release: "../dev", item: "helper", source: source},
		{name: "component traversal", release: "dev", item: "../helper", source: source},
		{name: "component separator", release: "dev", item: "nested/helper", source: source},
		{name: "directory source", release: "dev", item: "helper", source: t.TempDir()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Cache(test.release, test.item, test.source); err == nil {
				t.Fatal("unsafe component cache input was accepted")
			}
		})
	}
	if runtime.GOOS != "windows" {
		symlink := filepath.Join(t.TempDir(), "component-link")
		if err := os.Symlink(source, symlink); err != nil {
			t.Fatal(err)
		}
		if _, err := Cache("dev", "helper", symlink); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("symlink Cache() error = %v", err)
		}
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
