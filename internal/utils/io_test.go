package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteFileCreatesAndReplacesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	if err := WriteFile(path, []byte("first"), 0o700, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("second"), 0o700, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "second" {
		t.Fatalf("content = %q, want second", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestCleanupTempsRemovesOnlyMatchingRegularFiles(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "transfers.json")
	stale := filepath.Join(directory, ".transfers.json.tmp-stale")
	unrelated := filepath.Join(directory, ".servers.json.tmp-stale")
	for _, candidate := range []string{stale, unrelated} {
		if err := os.WriteFile(candidate, []byte("temporary"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := CleanupTemps(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("matching temporary file remains: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated temporary file was removed: %v", err)
	}
}
