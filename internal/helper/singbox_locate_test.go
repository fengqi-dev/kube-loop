package helper

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveSingBoxPathUsesConfiguredTrustedCore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sing-box")
	if err := os.WriteFile(path, []byte("core"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveSingBoxPath(AuthFile{SingBoxPath: path})
	if err != nil || got != filepath.Clean(path) {
		t.Fatalf("resolveSingBoxPath() = %q, %v", got, err)
	}
}

func TestValidateTrustedSingBoxRejectsInvalidFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if _, err := validateTrustedSingBox(directory); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory error = %v", err)
	}
	if runtime.GOOS == goosWindows {
		return
	}
	path := filepath.Join(t.TempDir(), "sing-box")
	if err := os.WriteFile(path, []byte("core"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateTrustedSingBox(path); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("non-executable error = %v", err)
	}
}

func TestTrustedSingBoxCandidatesPreserveLegacyHelperLayout(t *testing.T) {
	t.Parallel()
	executable := filepath.Join(t.TempDir(), "helpers", "kubeloop-helper")
	candidates := trustedSingBoxCandidates(AuthFile{SingBoxPath: "/protected/sing-box"}, executable)
	name := "sing-box"
	if runtime.GOOS == goosWindows {
		name = "sing-box.exe"
	}
	want := []string{
		"/protected/sing-box",
		filepath.Join(filepath.Dir(executable), name),
		filepath.Join(filepath.Dir(executable), "..", name),
		filepath.Join(filepath.Dir(executable), "Resources", name),
		filepath.Join(filepath.Dir(executable), "..", "Resources", name),
	}
	if strings.Join(candidates, "\n") != strings.Join(want, "\n") {
		t.Fatalf("trusted candidates = %#v, want %#v", candidates, want)
	}
}
