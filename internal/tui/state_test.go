package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewStateInitializesDevelopmentUserLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	state, err := NewState("dev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(state.Close)
	root := filepath.Join(home, ".kubeloop-dev")
	for _, directory := range []string{"config", "data", "state", "secrets", "cache"} {
		info, err := os.Stat(filepath.Join(root, directory))
		if err != nil || !info.IsDir() {
			t.Fatalf("development directory %q is unavailable: %v", directory, err)
		}
	}
}
