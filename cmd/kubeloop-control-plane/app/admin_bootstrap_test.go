package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadInitialPasswordFile(t *testing.T) {
	want := []byte("correct-horse-battery-staple")
	path := filepath.Join(t.TempDir(), "initial-password")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readInitialPasswordFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("initial password changed: got %q, want %q", got, want)
	}
}
