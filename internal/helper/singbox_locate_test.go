package helper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/componentstore"
)

func TestLocateBundledSingBoxPrefersFreshDevelopmentBuild(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	previousVersion := Version
	t.Cleanup(func() { Version = previousVersion })
	Version = developmentVersion

	fresh := filepath.Join(root, "build", "bin", singBoxTestName())
	if err := os.MkdirAll(filepath.Dir(fresh), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSingBoxTestFile(t, fresh, "fresh")
	stale := filepath.Join(t.TempDir(), singBoxTestName())
	writeSingBoxTestFile(t, stale, "stale")
	if _, err := componentstore.Cache(Version, singBoxTestName(), stale); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	got, err := LocateBundledSingBox()
	if err != nil {
		t.Fatal(err)
	}
	if got != fresh {
		t.Fatalf("LocateBundledSingBox() = %q, want fresh development build %q", got, fresh)
	}
}

func singBoxTestName() string {
	if filepath.Separator == '\\' {
		return "sing-box.exe"
	}
	return "sing-box"
}

func writeSingBoxTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
}
